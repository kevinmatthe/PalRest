use reqwest::{Client, Response, StatusCode, redirect::Policy};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::time::Duration;
use url::Url;

use crate::config::parse_base_url;

pub const REQUEST_TIMEOUT: Duration = Duration::from_secs(5);
const CONNECT_TIMEOUT: Duration = Duration::from_secs(3);
const MAX_REDIRECTS: usize = 3;
pub const MAX_BODY_BYTES: usize = 1024 * 1024;

#[derive(Clone)]
pub struct HttpBridge {
    client: Client,
}

#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SnapshotRequest {
    pub base_url: String,
    pub game_id: String,
    pub user_id: String,
    pub etag: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(untagged)]
pub enum SnapshotResult {
    Ok {
        status: u16,
        #[serde(skip_serializing_if = "Option::is_none")]
        etag: Option<String>,
        body: Value,
    },
    Status {
        status: u16,
    },
    Error {
        status: u16,
        code: String,
    },
}

#[derive(Debug, Serialize, Deserialize)]
pub struct PlayerListItem {
    pub user_id: String,
    pub name: String,
    pub account_name: String,
}

#[derive(Deserialize)]
struct PlayerList {
    players: Vec<PlayerListItem>,
}

impl HttpBridge {
    pub fn new() -> Result<Self, String> {
        let client = Client::builder()
            .connect_timeout(CONNECT_TIMEOUT)
            .timeout(REQUEST_TIMEOUT)
            .redirect(Policy::limited(MAX_REDIRECTS))
            .build()
            .map_err(|_| "could not create HTTP client".to_string())?;
        Ok(Self { client })
    }

    pub async fn fetch_snapshot(&self, request: SnapshotRequest) -> Result<SnapshotResult, String> {
        let mut url = endpoint(&request.base_url, "/api/v1/overlay/snapshot")?;
        url.query_pairs_mut()
            .append_pair("game_id", &request.game_id)
            .append_pair("user_id", &request.user_id);
        let mut builder = self.client.get(url);
        if let Some(etag) = request.etag.filter(|value| !value.contains(['\r', '\n'])) {
            builder = builder.header(reqwest::header::IF_NONE_MATCH, etag);
        }
        let response = builder
            .send()
            .await
            .map_err(|_| "snapshot request failed".to_string())?;
        let status = response.status();
        let etag = response
            .headers()
            .get(reqwest::header::ETAG)
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned);
        match map_status(status.as_u16())? {
            200 => {
                let body = read_json(response).await?;
                Ok(SnapshotResult::Ok {
                    status: 200,
                    etag,
                    body,
                })
            }
            304 => Ok(SnapshotResult::Status { status: 304 }),
            404 => {
                let body = read_json(response).await?;
                let code = body
                    .pointer("/error/code")
                    .and_then(Value::as_str)
                    .unwrap_or("player_not_found");
                let code = if code == "game_not_supported" {
                    code
                } else {
                    "player_not_found"
                };
                Ok(SnapshotResult::Error {
                    status: 404,
                    code: code.into(),
                })
            }
            503 => Ok(SnapshotResult::Error {
                status: 503,
                code: "snapshot_unavailable".into(),
            }),
            _ => unreachable!(),
        }
    }

    pub async fn list_players(&self, base_url: String) -> Result<Vec<PlayerListItem>, String> {
        let response = self
            .client
            .get(endpoint(&base_url, "/api/v1/players")?)
            .send()
            .await
            .map_err(|_| "player list request failed".to_string())?;
        if response.status() != StatusCode::OK {
            return Err(format!(
                "player list returned HTTP {}",
                response.status().as_u16()
            ));
        }
        let body = read_json(response).await?;
        serde_json::from_value::<PlayerList>(body)
            .map(|list| list.players)
            .map_err(|_| "player list response was invalid".to_string())
    }
}

pub fn map_status(status: u16) -> Result<u16, String> {
    match status {
        200 | 304 | 404 | 503 => Ok(status),
        _ => Err(format!("unexpected HTTP status {status}")),
    }
}

fn endpoint(base_url: &str, path: &str) -> Result<Url, String> {
    let mut base = parse_base_url(base_url).map_err(str::to_owned)?;
    base.set_path(path);
    Ok(base)
}

async fn read_json(mut response: Response) -> Result<Value, String> {
    if response
        .content_length()
        .is_some_and(|length| length > MAX_BODY_BYTES as u64)
    {
        return Err("response body exceeded 1 MiB".into());
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "response body read failed".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > MAX_BODY_BYTES {
            return Err("response body exceeded 1 MiB".into());
        }
        bytes.extend_from_slice(&chunk);
    }
    serde_json::from_slice(&bytes).map_err(|_| "response body was invalid JSON".to_string())
}

#[cfg(test)]
mod tests {
    use super::{HttpBridge, MAX_BODY_BYTES, REQUEST_TIMEOUT, endpoint, map_status};
    use std::time::Duration;

    #[test]
    fn maps_supported_statuses() {
        assert_eq!(map_status(200).unwrap(), 200);
        assert_eq!(map_status(304).unwrap(), 304);
        assert_eq!(map_status(404).unwrap(), 404);
        assert_eq!(map_status(503).unwrap(), 503);
        assert!(map_status(500).is_err());
    }

    #[test]
    fn client_uses_five_second_request_timeout() {
        assert_eq!(REQUEST_TIMEOUT, Duration::from_secs(5));
        HttpBridge::new().unwrap();
    }

    #[test]
    fn response_body_limit_is_one_mebibyte() {
        assert_eq!(MAX_BODY_BYTES, 1024 * 1024);
    }

    #[test]
    fn endpoints_reject_raw_dot_segment_paths() {
        assert!(endpoint("https://palbox.test/a/..", "/api/v1/players").is_err());
        assert!(endpoint("https://palbox.test/%2e", "/api/v1/players").is_err());
    }
}
