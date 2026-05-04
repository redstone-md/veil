// Veil Installer — admin HTTP API client.
//
// Talks to a Veil server's embedded admin endpoints (`internal/admin`
// in the core) over HTTP Basic auth. The installer treats each
// previously-deployed server as a managed object addressable through
// this client; the JS layer drives the actual UX.

use anyhow::{anyhow, Context, Result};
use base64::{engine::general_purpose::STANDARD as B64, Engine as _};
use serde::{Deserialize, Serialize};
use serde_json::Value;

#[derive(Debug, Clone, Deserialize)]
pub struct ServerCreds {
    /// e.g. "https://example.com:9090" or "http://1.2.3.4:9090".
    pub base_url: String,
    pub username: String,
    pub password: String,
}

#[derive(Debug, Serialize)]
pub struct VersionInfo {
    pub version: String,
    pub commit: String,
    pub date: String,
}

/// Probe `/api/version`. No auth required — succeeds against any
/// reachable Veil admin endpoint.
pub async fn version(creds: &ServerCreds) -> Result<VersionInfo> {
    let url = format!("{}/api/version", creds.base_url.trim_end_matches('/'));
    let resp = client()?.get(&url).send().await.context("admin: GET version")?;
    if !resp.status().is_success() {
        let status = resp.status();
        let body = resp.text().await.unwrap_or_default();
        return Err(anyhow!("HTTP {status}: {body}"));
    }
    let v: Value = resp.json().await.context("admin: decode version")?;
    Ok(VersionInfo {
        version: v.get("version").and_then(|s| s.as_str()).unwrap_or("").into(),
        commit:  v.get("commit") .and_then(|s| s.as_str()).unwrap_or("").into(),
        date:    v.get("date")   .and_then(|s| s.as_str()).unwrap_or("").into(),
    })
}

/// GET /api/dashboard — auth-required server snapshot
/// (active users, throughput, uptime, ...). Shape pass-through; the
/// frontend consumes the JSON directly so the schema can evolve
/// server-side without bumping this client.
pub async fn server_info(creds: &ServerCreds) -> Result<Value> {
    let url = format!("{}/api/server-info", creds.base_url.trim_end_matches('/'));
    let resp = client()?
        .get(&url)
        .header("Authorization", basic(creds))
        .send()
        .await
        .context("admin: GET server-info")?;
    handle(resp).await
}

pub async fn dashboard(creds: &ServerCreds) -> Result<Value> {
    let url = format!("{}/api/dashboard", creds.base_url.trim_end_matches('/'));
    let resp = client()?
        .get(&url)
        .header("Authorization", basic(creds))
        .send()
        .await
        .context("admin: GET dashboard")?;
    handle(resp).await
}

pub async fn users_list(creds: &ServerCreds) -> Result<Value> {
    let url = format!("{}/api/users", creds.base_url.trim_end_matches('/'));
    let resp = client()?
        .get(&url)
        .header("Authorization", basic(creds))
        .send()
        .await
        .context("admin: GET users")?;
    handle(resp).await
}

pub async fn user_add(creds: &ServerCreds, name: &str, pubkey_b64: Option<&str>) -> Result<Value> {
    let url = format!("{}/api/users", creds.base_url.trim_end_matches('/'));
    let body = serde_json::json!({
        "name": name,
        "pubkey_b64": pubkey_b64.unwrap_or(""),
    });
    let resp = client()?
        .post(&url)
        .header("Authorization", basic(creds))
        .header("Content-Type", "application/json")
        .body(body.to_string())
        .send()
        .await
        .context("admin: POST users")?;
    handle(resp).await
}

pub async fn user_delete(creds: &ServerCreds, id: &str) -> Result<()> {
    let url = format!("{}/api/users/{}", creds.base_url.trim_end_matches('/'), id);
    let resp = client()?
        .delete(&url)
        .header("Authorization", basic(creds))
        .send()
        .await
        .context("admin: DELETE user")?;
    if !resp.status().is_success() {
        let status = resp.status();
        let body = resp.text().await.unwrap_or_default();
        return Err(anyhow!("HTTP {status}: {body}"));
    }
    Ok(())
}

pub async fn user_update(creds: &ServerCreds, id: &str, patch: Value) -> Result<Value> {
    let url = format!("{}/api/users/{}", creds.base_url.trim_end_matches('/'), id);
    let resp = client()?
        .patch(&url)
        .header("Authorization", basic(creds))
        .header("Content-Type", "application/json")
        .body(patch.to_string())
        .send()
        .await
        .context("admin: PATCH user")?;
    handle(resp).await
}

fn basic(c: &ServerCreds) -> String {
    format!(
        "Basic {}",
        B64.encode(format!("{}:{}", c.username, c.password))
    )
}

fn client() -> Result<reqwest::Client> {
    reqwest::Client::builder()
        // Self-signed certs are common during the first month of a
        // deployment; trust the operator's stated host.
        .danger_accept_invalid_certs(true)
        .timeout(std::time::Duration::from_secs(15))
        .build()
        .context("admin: build http client")
}

async fn handle(resp: reqwest::Response) -> Result<Value> {
    let status = resp.status();
    let body = resp.text().await.unwrap_or_default();
    if !status.is_success() {
        return Err(anyhow!("HTTP {status}: {body}"));
    }
    if body.is_empty() {
        return Ok(Value::Null);
    }
    serde_json::from_str(&body).context("admin: decode JSON")
}
