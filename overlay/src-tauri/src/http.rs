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

#[derive(Clone, Copy)]
struct HttpPolicy {
    connect_timeout: Duration,
    request_timeout: Duration,
    max_redirects: usize,
    max_body_bytes: usize,
    use_system_proxy: bool,
}

impl Default for HttpPolicy {
    fn default() -> Self {
        Self {
            connect_timeout: CONNECT_TIMEOUT,
            request_timeout: REQUEST_TIMEOUT,
            max_redirects: MAX_REDIRECTS,
            max_body_bytes: MAX_BODY_BYTES,
            use_system_proxy: true,
        }
    }
}

#[derive(Clone)]
pub struct HttpBridge {
    client: Client,
    max_body_bytes: usize,
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
        Self::new_with_policy(HttpPolicy::default())
    }

    fn new_with_policy(policy: HttpPolicy) -> Result<Self, String> {
        let mut builder = Client::builder()
            .connect_timeout(policy.connect_timeout)
            .timeout(policy.request_timeout)
            .redirect(Policy::limited(policy.max_redirects));
        if !policy.use_system_proxy {
            builder = builder.no_proxy();
        }
        let client = builder
            .build()
            .map_err(|_| "could not create HTTP client".to_string())?;
        Ok(Self {
            client,
            max_body_bytes: policy.max_body_bytes,
        })
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
                let body = read_json(response, self.max_body_bytes).await?;
                Ok(SnapshotResult::Ok {
                    status: 200,
                    etag,
                    body,
                })
            }
            304 => Ok(SnapshotResult::Status { status: 304 }),
            404 => {
                let body = read_json(response, self.max_body_bytes).await?;
                let code = body
                    .pointer("/error/code")
                    .and_then(Value::as_str)
                    .filter(|code| matches!(*code, "player_not_found" | "game_not_supported"))
                    .ok_or_else(|| "invalid_response: unknown 404 error code".to_string())?;
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
        let body = read_json(response, self.max_body_bytes).await?;
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

async fn read_json(mut response: Response, max_body_bytes: usize) -> Result<Value, String> {
    if response
        .content_length()
        .is_some_and(|length| length > max_body_bytes as u64)
    {
        return Err("response body exceeded 1 MiB".into());
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "response body read failed".to_string())?
    {
        if bytes.len().saturating_add(chunk.len()) > max_body_bytes {
            return Err("response body exceeded 1 MiB".into());
        }
        bytes.extend_from_slice(&chunk);
    }
    serde_json::from_slice(&bytes).map_err(|_| "response body was invalid JSON".to_string())
}

#[cfg(test)]
mod tests {
    use super::{
        HttpBridge, HttpPolicy, MAX_BODY_BYTES, REQUEST_TIMEOUT, SnapshotRequest, endpoint,
        map_status,
    };
    use std::{
        io::{Read, Write},
        net::TcpListener,
        thread,
        time::Duration,
    };

    struct Reply {
        bytes: Vec<u8>,
        hold_open: Duration,
    }

    fn serve(replies: Vec<Reply>) -> (String, thread::JoinHandle<()>) {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        let handle = thread::spawn(move || {
            for reply in replies {
                let (mut stream, _) = listener.accept().unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(1)))
                    .unwrap();
                let mut request = [0_u8; 4096];
                let _ = stream.read(&mut request);
                if !reply.bytes.is_empty() {
                    stream.write_all(&reply.bytes).unwrap();
                    stream.flush().unwrap();
                }
                thread::sleep(reply.hold_open);
            }
        });
        (format!("http://{address}"), handle)
    }

    fn response(status: &str, headers: &str, body: &str) -> Vec<u8> {
        format!(
            "HTTP/1.1 {status}\r\nConnection: close\r\n{headers}Content-Length: {}\r\n\r\n{body}",
            body.len()
        )
        .into_bytes()
    }

    fn policy(max_body_bytes: usize) -> HttpPolicy {
        HttpPolicy {
            connect_timeout: Duration::from_millis(500),
            request_timeout: Duration::from_secs(1),
            max_redirects: 1,
            max_body_bytes,
            use_system_proxy: false,
        }
    }

    fn timeout_policy() -> HttpPolicy {
        HttpPolicy {
            request_timeout: Duration::from_millis(100),
            ..policy(1024)
        }
    }

    fn request(base_url: String) -> SnapshotRequest {
        SnapshotRequest {
            base_url,
            game_id: "palworld".into(),
            user_id: "uid".into(),
            etag: None,
        }
    }

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

    #[tokio::test]
    async fn maps_real_snapshot_responses_and_preserves_etag_and_error_code() {
        let cases = [
            (
                "200 OK",
                "ETag: \"v1\"\r\n",
                "{}",
                200,
                Some("\"v1\""),
                None,
            ),
            ("304 Not Modified", "", "", 304, None, None),
            (
                "404 Not Found",
                "Content-Type: application/json\r\n",
                r#"{"error":{"code":"game_not_supported"}}"#,
                404,
                None,
                Some("game_not_supported"),
            ),
            (
                "503 Service Unavailable",
                "",
                "",
                503,
                None,
                Some("snapshot_unavailable"),
            ),
        ];
        for (status, headers, body, expected_status, expected_etag, expected_code) in cases {
            let (base_url, server) = serve(vec![Reply {
                bytes: response(status, headers, body),
                hold_open: Duration::ZERO,
            }]);
            let bridge = HttpBridge::new_with_policy(policy(1024)).unwrap();
            let result = bridge.fetch_snapshot(request(base_url)).await.unwrap();
            let value = serde_json::to_value(result).unwrap();
            assert_eq!(value["status"], expected_status);
            assert_eq!(value.get("etag").and_then(|v| v.as_str()), expected_etag);
            assert_eq!(value.get("code").and_then(|v| v.as_str()), expected_code);
            server.join().unwrap();
        }
    }

    #[tokio::test]
    async fn rejects_404_without_a_known_error_code_as_invalid_response() {
        for body in [r#"{"error":{}}"#, r#"{"error":{"code":"unknown"}}"#] {
            let (base_url, server) = serve(vec![Reply {
                bytes: response("404 Not Found", "Content-Type: application/json\r\n", body),
                hold_open: Duration::ZERO,
            }]);
            let bridge = HttpBridge::new_with_policy(policy(1024)).unwrap();
            let error = bridge.fetch_snapshot(request(base_url)).await.unwrap_err();
            assert!(error.contains("invalid_response"), "{error}");
            server.join().unwrap();
        }
    }

    #[tokio::test]
    async fn enforces_body_limit_for_chunked_and_declared_oversized_responses() {
        let chunked = format!(
            "HTTP/1.1 200 OK\r\nConnection: close\r\nTransfer-Encoding: chunked\r\n\r\n28\r\n{}\r\n0\r\n\r\n",
            "a".repeat(40)
        )
        .into_bytes();
        let declared =
            b"HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 100\r\n\r\n{}".to_vec();
        for bytes in [chunked, declared] {
            let (base_url, server) = serve(vec![Reply {
                bytes,
                hold_open: Duration::ZERO,
            }]);
            let bridge = HttpBridge::new_with_policy(policy(32)).unwrap();
            let error = bridge.fetch_snapshot(request(base_url)).await.unwrap_err();
            assert!(error.contains("exceeded"), "{error}");
            server.join().unwrap();
        }
    }

    #[tokio::test]
    async fn times_out_stalled_headers_and_stalled_bodies() {
        let stalled_header = Reply {
            bytes: vec![],
            hold_open: Duration::from_millis(250),
        };
        let stalled_body = Reply {
            bytes: b"HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\n{".to_vec(),
            hold_open: Duration::from_millis(250),
        };
        for reply in [stalled_header, stalled_body] {
            let (base_url, server) = serve(vec![reply]);
            let bridge = HttpBridge::new_with_policy(timeout_policy()).unwrap();
            assert!(bridge.fetch_snapshot(request(base_url)).await.is_err());
            server.join().unwrap();
        }
    }

    #[tokio::test]
    async fn rejects_redirects_beyond_the_configured_limit() {
        let redirect = |location: &str| Reply {
            bytes: response("302 Found", &format!("Location: {location}\r\n"), ""),
            hold_open: Duration::ZERO,
        };
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let base_url = format!("http://{}", listener.local_addr().unwrap());
        let location = format!("{base_url}/again");
        let handle = thread::spawn(move || {
            listener.set_nonblocking(true).unwrap();
            let deadline = std::time::Instant::now() + Duration::from_millis(250);
            while std::time::Instant::now() < deadline {
                match listener.accept() {
                    Ok((mut stream, _)) => {
                        let mut request = [0_u8; 4096];
                        let _ = stream.read(&mut request);
                        stream.write_all(&redirect(&location).bytes).unwrap();
                    }
                    Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                        thread::sleep(Duration::from_millis(5));
                    }
                    Err(error) => panic!("redirect test server failed: {error}"),
                }
            }
        });
        let bridge = HttpBridge::new_with_policy(policy(1024)).unwrap();
        assert!(bridge.fetch_snapshot(request(base_url)).await.is_err());
        handle.join().unwrap();
    }
}
