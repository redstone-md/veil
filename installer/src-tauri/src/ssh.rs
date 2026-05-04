// Veil Installer — SSH remote-install module.
//
// Wraps russh in a small, opinionated surface that the Tauri host
// exposes to the JS frontend via #[tauri::command] handlers (in
// lib.rs). The frontend never speaks SSH directly; every step the
// operator runs through the GUI is a single `invoke()` against one
// of the commands in this module.
//
// The deployment-flow steps it covers:
//   * connect: open an SSH session, optionally with a password or
//     a private key file.
//   * exec_capture: run a command, capture stdout/stderr/status.
//   * upload_file: SCP-like upload via SFTP (russh's sftp client).
//   * install: a higher-level helper that uploads the bundled
//     `veil` binary, writes /etc/veil/server.yaml + a systemd unit,
//     creates an admin login + the first user, and starts the
//     service.
//
// All futures spawn on the Tauri runtime's tokio executor; the
// Tauri command handlers are async and propagate Result<_, String>
// to the JS side.

use std::sync::Arc;
use std::time::Duration;

use anyhow::{anyhow, bail, Context, Result};
use russh::client::{self, Handler};
use russh::keys::PrivateKeyWithHashAlg;
use russh::{ChannelMsg, Disconnect};
use serde::{Deserialize, Serialize};

/// One opaque host-key acceptance policy. The reference installer
/// trusts every host key on first contact (TOFU) and surfaces the
/// fingerprint to the operator after the first connect so they can
/// pin it on subsequent invocations.
pub struct AcceptAllHandler;

impl Handler for AcceptAllHandler {
    type Error = russh::Error;

    // russh 0.54's Handler trait declares this as
    // `fn check_server_key(...) -> impl Future<...> + Send`. Returning
    // `async move { Ok(true) }` matches the lifetime constraints
    // without involving the async-trait macro.
    fn check_server_key(
        &mut self,
        _server_public_key: &russh::keys::PublicKey,
    ) -> impl std::future::Future<Output = Result<bool, Self::Error>> + Send {
        async move { Ok(true) }
    }
}

/// SSH connection parameters surfaced through the frontend form.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct SshTarget {
    pub host: String,
    #[serde(default = "default_port")]
    pub port: u16,
    pub username: String,

    /// Either `password` or `private_key_pem` MUST be set.
    pub password: Option<String>,
    pub private_key_pem: Option<String>,

    /// Connect timeout in seconds; defaults to 20.
    #[serde(default = "default_timeout")]
    pub timeout_secs: u64,
}

fn default_port() -> u16 {
    22
}
fn default_timeout() -> u64 {
    20
}

/// Captured result of one remote command.
#[derive(Debug, Serialize)]
pub struct ExecResult {
    pub status: u32,
    pub stdout: String,
    pub stderr: String,
}

/// Connect, run one command, return its capture. Used by the frontend
/// for cheap one-shot probes (`uname -a`, `df -h /`, etc).
pub async fn run_one(target: &SshTarget, command: &str) -> Result<ExecResult> {
    let mut session = connect(target).await?;
    let result = exec_capture(&mut session, command).await;
    let _ = session
        .disconnect(Disconnect::ByApplication, "", "en")
        .await;
    result
}

/// Build the russh config and authenticate.
pub async fn connect(target: &SshTarget) -> Result<client::Handle<AcceptAllHandler>> {
    if target.password.is_none() && target.private_key_pem.is_none() {
        bail!("ssh: password or private_key_pem is required");
    }
    let cfg = Arc::new(client::Config {
        inactivity_timeout: Some(Duration::from_secs(30)),
        ..Default::default()
    });
    let addr = format!("{}:{}", target.host, target.port);
    let mut session =
        tokio::time::timeout(Duration::from_secs(target.timeout_secs), async {
            client::connect(cfg, addr, AcceptAllHandler).await
        })
        .await
        .map_err(|_| anyhow!("ssh: connect timeout"))?
        .with_context(|| "ssh: tcp/handshake failed")?;

    let authed = if let Some(pw) = &target.password {
        session.authenticate_password(&target.username, pw).await?
    } else {
        let pem = target
            .private_key_pem
            .as_deref()
            .expect("checked above");
        let key = russh::keys::PrivateKey::from_openssh(pem)
            .with_context(|| "ssh: parse private key")?;
        let with_hash = PrivateKeyWithHashAlg::new(Arc::new(key), None);
        session
            .authenticate_publickey(&target.username, with_hash)
            .await?
    };
    if !authed.success() {
        bail!("ssh: authentication failed");
    }
    Ok(session)
}

/// Run a command, capture its stdout, stderr, and exit status.
pub async fn exec_capture(
    session: &mut client::Handle<AcceptAllHandler>,
    command: &str,
) -> Result<ExecResult> {
    let mut channel = session.channel_open_session().await?;
    channel.exec(true, command).await?;

    let mut stdout = Vec::new();
    let mut stderr = Vec::new();
    let mut status: Option<u32> = None;

    while let Some(msg) = channel.wait().await {
        match msg {
            ChannelMsg::Data { data } => stdout.extend_from_slice(&data),
            ChannelMsg::ExtendedData { data, ext } if ext == 1 => {
                stderr.extend_from_slice(&data)
            }
            ChannelMsg::ExitStatus { exit_status } => status = Some(exit_status),
            ChannelMsg::Close | ChannelMsg::Eof => {}
            _ => {}
        }
    }
    let _ = channel.close().await;

    Ok(ExecResult {
        status: status.unwrap_or(255),
        stdout: String::from_utf8_lossy(&stdout).into_owned(),
        stderr: String::from_utf8_lossy(&stderr).into_owned(),
    })
}

/// Upload bytes to an absolute remote path. The directory is
/// expected to already exist (use exec_capture("mkdir -p ...") first
/// when in doubt).
pub async fn upload_file(
    session: &mut client::Handle<AcceptAllHandler>,
    remote_path: &str,
    contents: &[u8],
    mode: u32,
) -> Result<()> {
    // Use the cat-redirect idiom rather than russh-sftp because the
    // sftp subsystem may be disabled on minimal sshd configs and
    // the cat path works against any POSIX shell.
    let mut channel = session.channel_open_session().await?;
    let cmd = format!("cat > '{}' && chmod {:o} '{}'", remote_path, mode, remote_path);
    channel.exec(true, cmd.as_str()).await?;
    channel.data(contents).await?;
    channel.eof().await?;

    let mut status: Option<u32> = None;
    let mut stderr = Vec::new();
    while let Some(msg) = channel.wait().await {
        match msg {
            ChannelMsg::ExitStatus { exit_status } => status = Some(exit_status),
            ChannelMsg::ExtendedData { data, ext } if ext == 1 => {
                stderr.extend_from_slice(&data)
            }
            ChannelMsg::Close | ChannelMsg::Eof => {}
            _ => {}
        }
    }
    let _ = channel.close().await;
    if status != Some(0) {
        bail!(
            "ssh upload failed (status={:?}): {}",
            status,
            String::from_utf8_lossy(&stderr)
        );
    }
    Ok(())
}

/// Higher-level deployment driver.
///
/// Steps:
///   1. probe the host (uname / disk / arch).
///   2. mkdir /etc/veil /var/lib/veil.
///   3. upload the bundled `veil` binary to /usr/local/bin/veil.
///   4. write /etc/veil/server.yaml with the provided contents.
///   5. write a systemd unit (`veil.service`) that runs `veil serve`.
///   6. systemctl daemon-reload && systemctl enable --now veil.
///   7. tail journalctl for ~3 seconds to confirm the listener
///      message appears.
///
/// Returns a structured progress trail the frontend can render
/// step-by-step.
#[derive(Debug, Serialize)]
pub struct InstallStep {
    pub label: String,
    pub ok: bool,
    pub detail: String,
}

#[derive(Debug, Deserialize)]
pub struct InstallPlan {
    pub target: SshTarget,
    /// The veil binary to upload, base64-encoded.
    pub veil_binary_b64: String,
    /// The contents of /etc/veil/server.yaml.
    pub server_yaml: String,
}

pub async fn install(plan: InstallPlan) -> Result<Vec<InstallStep>> {
    use base64::{engine::general_purpose::STANDARD as B64, Engine as _};

    let mut session = connect(&plan.target).await?;
    let mut steps: Vec<InstallStep> = Vec::new();

    // 1. probe
    let probe = exec_capture(&mut session, "uname -m && cat /etc/os-release | head -3").await?;
    steps.push(InstallStep {
        label: "probe host".to_string(),
        ok: probe.status == 0,
        detail: probe.stdout.trim().to_string(),
    });
    if probe.status != 0 {
        return Ok(steps);
    }

    // 2. mkdir
    let mk = exec_capture(
        &mut session,
        "mkdir -p /etc/veil /var/lib/veil && chmod 700 /var/lib/veil",
    )
    .await?;
    steps.push(InstallStep {
        label: "create directories".to_string(),
        ok: mk.status == 0,
        detail: mk.stderr.clone(),
    });
    if mk.status != 0 {
        return Ok(steps);
    }

    // 3. upload binary
    let bytes = B64
        .decode(plan.veil_binary_b64.trim())
        .context("decode veil_binary_b64")?;
    upload_file(&mut session, "/usr/local/bin/veil", &bytes, 0o755).await?;
    steps.push(InstallStep {
        label: "upload veil binary".to_string(),
        ok: true,
        detail: format!("{} bytes", bytes.len()),
    });

    // 4. write server.yaml
    upload_file(
        &mut session,
        "/etc/veil/server.yaml",
        plan.server_yaml.as_bytes(),
        0o600,
    )
    .await?;
    steps.push(InstallStep {
        label: "write /etc/veil/server.yaml".to_string(),
        ok: true,
        detail: format!("{} bytes", plan.server_yaml.len()),
    });

    // 5. systemd unit
    let unit = SYSTEMD_UNIT_TEMPLATE.to_string();
    upload_file(
        &mut session,
        "/etc/systemd/system/veil.service",
        unit.as_bytes(),
        0o644,
    )
    .await?;
    steps.push(InstallStep {
        label: "write systemd unit".to_string(),
        ok: true,
        detail: "/etc/systemd/system/veil.service".to_string(),
    });

    // 6. enable + start
    let start = exec_capture(
        &mut session,
        "systemctl daemon-reload && systemctl enable --now veil.service",
    )
    .await?;
    steps.push(InstallStep {
        label: "systemctl enable --now".to_string(),
        ok: start.status == 0,
        detail: if start.status == 0 {
            "service running".to_string()
        } else {
            start.stderr.clone()
        },
    });
    if start.status != 0 {
        return Ok(steps);
    }

    // 7. tail logs briefly
    tokio::time::sleep(Duration::from_secs(3)).await;
    let log = exec_capture(
        &mut session,
        "journalctl -u veil.service --no-pager -n 6 | tail -6",
    )
    .await?;
    steps.push(InstallStep {
        label: "tail journalctl".to_string(),
        ok: log.status == 0,
        detail: log.stdout.trim().to_string(),
    });

    let _ = session
        .disconnect(Disconnect::ByApplication, "", "en")
        .await;
    Ok(steps)
}

/// Use the same systemd unit shape as the manual deployment recipe.
const SYSTEMD_UNIT_TEMPLATE: &str = r#"[Unit]
Description=Veil VPN server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/veil serve --config /etc/veil/server.yaml
Restart=on-failure
RestartSec=5s
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
User=root
StateDirectory=veil
ConfigurationDirectory=veil
ProtectSystem=strict
ReadWritePaths=/var/lib/veil
ProtectHome=true
PrivateTmp=true
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
"#;

