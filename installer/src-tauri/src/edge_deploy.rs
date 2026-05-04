// Veil Installer — direct API push to Deno Deploy and Fly.io.
//
// Phase 3.9 closes the Phase 3.7 TODO of "operator runs deployctl /
// fly deploy themselves". The two functions in this module take a
// personal-access token plus the params from the Edge form and
// drive the provider's REST API directly. The Tauri host wraps
// each function in a #[tauri::command] (in lib.rs).
//
// Design choice: token-paste, NOT browser OAuth. Browser OAuth
// requires registering an app with each provider (we'd have to
// publish + maintain client IDs), handling the callback URL
// inside Tauri (custom URL scheme + foreground reactivation), and
// secure storage for the resulting refresh tokens. Token-paste
// gets the operator the same outcome with a fraction of the
// surface area: they create a PAT in the provider's dashboard and
// hand us one short-lived secret per session.
//
// Trust model: the token never touches disk inside the installer
// (we hold it in a Tauri command parameter for the duration of one
// API call). The operator is expected to revoke the token from
// the provider dashboard once the deploy is done.

use anyhow::{anyhow, bail, Context, Result};
use base64::{engine::general_purpose::STANDARD as B64, Engine as _};
use serde::{Deserialize, Serialize};
use serde_json::json;

const DENO_API_BASE: &str = "https://api.deno.com";
const FLY_API_BASE: &str = "https://api.machines.dev";

/// Result returned to the frontend after a successful deploy.
#[derive(Debug, Serialize)]
pub struct DeployResult {
    pub url: String,
    pub provider: String,
    pub project: String,
    pub note: String,
}

// --- Deno Deploy ---------------------------------------------------

#[derive(Debug, Deserialize)]
pub struct DenoDeployParams {
    /// Personal-access token from https://dash.deno.com/account.
    pub token: String,
    /// Project slug (will be created if it does not exist).
    pub project: String,
    /// Origin host the worker forwards to.
    pub origin_host: String,
    pub origin_port: Option<u16>,
    /// URL path the worker accepts WSS upgrades on.
    pub path: Option<String>,
}

/// Deploy the Deno worker to Deno Deploy via the REST API.
///
/// Steps:
///   1. POST /v1/projects {name} — idempotent if the project already
///      exists (the API returns 409; we treat that as success).
///   2. POST /v1/projects/{id}/deployments with the worker source
///      in the assets map and the operator's env vars.
///
/// Returns the *.deno.dev URL the worker is reachable at.
pub async fn deploy_deno(params: DenoDeployParams) -> Result<DeployResult> {
    let port = params.origin_port.unwrap_or(443);
    let mut path = params.path.unwrap_or_else(|| "/ws".into());
    if !path.starts_with('/') {
        path.insert(0, '/');
    }
    let token = params.token.trim();
    if token.is_empty() {
        bail!("deno: token is required");
    }
    if params.project.trim().is_empty() {
        bail!("deno: project slug is required");
    }

    let client = http_client()?;

    // 1. ensure project exists
    let project_id = ensure_deno_project(&client, token, &params.project).await?;

    // 2. build the deployment body
    let worker = include_str!("../../../deploy/edge/deno/worker.ts");
    let assets = json!({
        "worker.ts": {
            "kind": "file",
            "content": B64.encode(worker),
            "encoding": "base64",
        }
    });
    let body = json!({
        "entryPointUrl": "worker.ts",
        "assets": assets,
        "envVars": {
            "VEIL_ORIGIN_HOST": params.origin_host,
            "VEIL_ORIGIN_PORT": port.to_string(),
            "VEIL_PATH": path,
        },
        "productionDeployment": true,
    });

    let resp = client
        .post(format!("{DENO_API_BASE}/v1/projects/{project_id}/deployments"))
        .bearer_auth(token)
        .header("Content-Type", "application/json")
        .body(body.to_string())
        .send()
        .await
        .context("deno: deployment request")?;
    let status = resp.status();
    let text = resp.text().await.unwrap_or_default();
    if !status.is_success() {
        bail!("deno: deployment failed: HTTP {status}: {text}");
    }

    Ok(DeployResult {
        provider: "deno".into(),
        project: params.project.clone(),
        url: format!("https://{}.deno.dev{}", params.project, path),
        note: "Deployment kicked off; the worker is live within 10-30 seconds.".into(),
    })
}

async fn ensure_deno_project(
    client: &reqwest::Client,
    token: &str,
    name: &str,
) -> Result<String> {
    // Try to look up first; create if missing.
    let resp = client
        .get(format!("{DENO_API_BASE}/v1/projects/{name}"))
        .bearer_auth(token)
        .send()
        .await
        .context("deno: project lookup")?;
    if resp.status().is_success() {
        let body: serde_json::Value = resp.json().await.context("deno: lookup body")?;
        return body
            .get("id")
            .and_then(|v| v.as_str())
            .map(String::from)
            .ok_or_else(|| anyhow!("deno: lookup body missing id"));
    }
    if resp.status().as_u16() != 404 {
        let s = resp.status();
        let t = resp.text().await.unwrap_or_default();
        bail!("deno: project lookup HTTP {s}: {t}");
    }

    // Create.
    let resp = client
        .post(format!("{DENO_API_BASE}/v1/projects"))
        .bearer_auth(token)
        .header("Content-Type", "application/json")
        .body(json!({ "name": name }).to_string())
        .send()
        .await
        .context("deno: project create")?;
    if !resp.status().is_success() {
        let s = resp.status();
        let t = resp.text().await.unwrap_or_default();
        bail!("deno: project create HTTP {s}: {t}");
    }
    let body: serde_json::Value = resp.json().await.context("deno: create body")?;
    body.get("id")
        .and_then(|v| v.as_str())
        .map(String::from)
        .ok_or_else(|| anyhow!("deno: create body missing id"))
}

// --- Fly.io --------------------------------------------------------

#[derive(Debug, Deserialize)]
pub struct FlyDeployParams {
    /// Token from `fly auth token` (Bearer, not API token).
    pub token: String,
    /// App name; created if it does not exist.
    pub app: String,
    /// Org slug under which to create the app (default: "personal").
    pub org: Option<String>,
    /// Region code (default: "fra"); see fly platform regions.
    pub region: Option<String>,
    /// Origin host the worker forwards to.
    pub origin_host: String,
    pub origin_port: Option<u16>,
    pub path: Option<String>,
    /// OCI image to run; defaults to the project's published edge
    /// image at ghcr.io. Operators with a private build can pass
    /// their own image reference here.
    pub image: Option<String>,
}

/// Deploy the Fly edge worker via the Machines API.
///
/// Steps:
///   1. POST /v1/apps {app_name, org_slug} — 201 Created or 422 if
///      the name is already taken (treated as success when the
///      existing app belongs to the operator).
///   2. POST /v1/apps/{name}/machines with the image config + env
///      vars + a single internal HTTP service on port 8080.
///
/// The operator is expected to point DNS at fly's *.fly.dev or
/// register their own custom domain via the Fly dashboard later.
pub async fn deploy_fly(params: FlyDeployParams) -> Result<DeployResult> {
    let port = params.origin_port.unwrap_or(443);
    let mut path = params.path.unwrap_or_else(|| "/ws".into());
    if !path.starts_with('/') {
        path.insert(0, '/');
    }
    let token = params.token.trim();
    if token.is_empty() {
        bail!("fly: token is required");
    }
    if params.app.trim().is_empty() {
        bail!("fly: app name is required");
    }
    let org = params.org.unwrap_or_else(|| "personal".into());
    let region = params.region.unwrap_or_else(|| "fra".into());
    let image = params
        .image
        .unwrap_or_else(|| "ghcr.io/redstone-md/veil-edge:latest".into());

    let client = http_client()?;

    // 1. create app (idempotent on 422)
    let resp = client
        .post(format!("{FLY_API_BASE}/v1/apps"))
        .bearer_auth(token)
        .header("Content-Type", "application/json")
        .body(json!({ "app_name": params.app, "org_slug": org }).to_string())
        .send()
        .await
        .context("fly: app create")?;
    let status = resp.status();
    if !status.is_success() && status.as_u16() != 422 {
        let t = resp.text().await.unwrap_or_default();
        bail!("fly: app create HTTP {status}: {t}");
    }

    // 2. start machine
    let machine_body = json!({
        "config": {
            "image": image,
            "env": {
                "VEIL_LISTEN":      ":8080",
                "VEIL_ORIGIN_HOST": params.origin_host,
                "VEIL_ORIGIN_PORT": port.to_string(),
                "VEIL_PATH":        path,
            },
            "services": [{
                "internal_port": 8080,
                "protocol": "tcp",
                "ports": [
                    { "port": 80,  "handlers": ["http"], "force_https": true },
                    { "port": 443, "handlers": ["tls", "http"] }
                ]
            }],
            "guest": {
                "cpu_kind": "shared",
                "cpus": 1,
                "memory_mb": 256
            },
            "auto_destroy": false,
        },
        "region": region,
    });
    let resp = client
        .post(format!("{FLY_API_BASE}/v1/apps/{}/machines", params.app))
        .bearer_auth(token)
        .header("Content-Type", "application/json")
        .body(machine_body.to_string())
        .send()
        .await
        .context("fly: machine create")?;
    let status = resp.status();
    let text = resp.text().await.unwrap_or_default();
    if !status.is_success() {
        bail!("fly: machine create HTTP {status}: {text}");
    }

    Ok(DeployResult {
        provider: "fly".into(),
        project: params.app.clone(),
        url: format!("https://{}.fly.dev{}", params.app, path),
        note: format!(
            "Machine started in {region}. The first request may take a few seconds while Fly cold-starts the VM."
        ),
    })
}

fn http_client() -> Result<reqwest::Client> {
    reqwest::Client::builder()
        .user_agent(concat!("veil-installer/", env!("CARGO_PKG_VERSION")))
        .build()
        .context("build http client")
}
