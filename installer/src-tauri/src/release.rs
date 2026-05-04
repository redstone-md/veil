// Veil Installer — GitHub Release lookup helper.
//
// Lets the SSH workflow download the matching `veil` binary from
// the project's latest tagged release rather than asking the
// operator to file-pick one. The chosen platform comes from a
// remote `uname -m` capture so the binary always matches the
// target VPS, not the operator's desktop.

use anyhow::{anyhow, bail, Context, Result};
use serde::{Deserialize, Serialize};

/// Default upstream repository.
pub const DEFAULT_REPO: &str = "redstone-md/veil";

/// Subset of the GitHub Releases API response.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct Release {
    pub tag_name: String,
    pub html_url: String,
    pub assets: Vec<Asset>,
}

/// One asset on a release.
#[derive(Debug, Deserialize, Serialize, Clone)]
pub struct Asset {
    pub name: String,
    pub size: u64,
    pub browser_download_url: String,
}

/// Fetch the `latest` release metadata from GitHub.
pub async fn latest(repo: &str) -> Result<Release> {
    let url = format!("https://api.github.com/repos/{repo}/releases/latest");
    let client = http_client()?;
    let resp = client
        .get(&url)
        .header("Accept", "application/vnd.github+json")
        .header("X-GitHub-Api-Version", "2022-11-28")
        .send()
        .await
        .with_context(|| format!("GET {url}"))?;
    if !resp.status().is_success() {
        let status = resp.status();
        let body = resp.text().await.unwrap_or_default();
        bail!("github releases: HTTP {status}: {body}");
    }
    let r: Release = resp.json().await.context("decode release JSON")?;
    Ok(r)
}

/// Translate a remote `uname -m` plus a platform hint into the
/// CI-canonical asset name.
///
/// The CI matrix emits names like:
///   veil-linux-amd64
///   veil-linux-arm64
///   veil-darwin-amd64
///   veil-darwin-arm64
///   veil-windows-amd64.exe
pub fn asset_name_for(uname_m: &str, target_os: TargetOS) -> Result<String> {
    let arch = match uname_m.trim() {
        "x86_64" | "amd64" => "amd64",
        "aarch64" | "arm64" => "arm64",
        other => bail!("unsupported architecture {other:?}"),
    };
    let base = match target_os {
        TargetOS::Linux => format!("veil-linux-{arch}"),
        TargetOS::Darwin => format!("veil-darwin-{arch}"),
        TargetOS::Windows => format!("veil-windows-{arch}.exe"),
    };
    Ok(base)
}

/// Coarse target-OS classification used to compose the asset name.
#[derive(Debug, Clone, Copy)]
pub enum TargetOS {
    Linux,
    Darwin,
    Windows,
}

impl TargetOS {
    pub fn detect_from_os_release(text: &str) -> Self {
        let lower = text.to_ascii_lowercase();
        if lower.contains("darwin") || lower.contains("macos") {
            return TargetOS::Darwin;
        }
        if lower.contains("windows") || lower.contains("microsoft") {
            return TargetOS::Windows;
        }
        TargetOS::Linux
    }
}

/// Look up the asset on the release whose name matches the target
/// platform.
pub fn pick_asset<'r>(release: &'r Release, asset_name: &str) -> Result<&'r Asset> {
    release
        .assets
        .iter()
        .find(|a| a.name == asset_name)
        .ok_or_else(|| {
            anyhow!(
                "no asset named {asset_name:?} on release {}",
                release.tag_name
            )
        })
}

/// Download an asset's contents into memory.
pub async fn download(asset: &Asset) -> Result<Vec<u8>> {
    let client = http_client()?;
    let resp = client
        .get(&asset.browser_download_url)
        .header("Accept", "application/octet-stream")
        .send()
        .await
        .with_context(|| format!("GET {}", asset.browser_download_url))?;
    if !resp.status().is_success() {
        let status = resp.status();
        bail!("download: HTTP {status}");
    }
    let bytes = resp.bytes().await.context("read asset body")?;
    Ok(bytes.to_vec())
}

fn http_client() -> Result<reqwest::Client> {
    reqwest::Client::builder()
        .user_agent(concat!("veil-installer/", env!("CARGO_PKG_VERSION")))
        .build()
        .context("build http client")
}
