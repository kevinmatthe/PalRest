use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    fs::{self, File},
    io::{self, Write},
    path::Path,
};
use url::Url;

const CONFIG_FILE: &str = "config.json";
const TEMP_FILE: &str = "config.json.tmp";

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct OverlayConfig {
    pub schema: u32,
    pub base_url: String,
    pub game_id: String,
    pub user_id: String,
    pub scale: f64,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub display_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub x: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub y: Option<f64>,
    pub locked: bool,
}

fn invalid_data(message: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message.into())
}

pub(crate) fn parse_base_url(raw: &str) -> Result<Url, &'static str> {
    if !raw
        .get(..raw.find("://").unwrap_or(0))
        .is_some_and(|scheme| {
            scheme.eq_ignore_ascii_case("http") || scheme.eq_ignore_ascii_case("https")
        })
        || raw.contains(['\\', '?', '#'])
        || raw.chars().any(char::is_control)
    {
        return Err("invalid base URL");
    }
    let authority_start = raw.find("://").ok_or("invalid base URL")? + 3;
    let raw_suffix = raw[authority_start..]
        .find('/')
        .map(|index| &raw[authority_start + index..])
        .unwrap_or("");
    if !matches!(raw_suffix, "" | "/") {
        return Err("invalid base URL");
    }
    let base = Url::parse(raw).map_err(|_| "invalid base URL")?;
    if !matches!(base.scheme(), "http" | "https")
        || base.host_str().is_none()
        || !base.username().is_empty()
        || base.password().is_some()
        || !matches!(base.path(), "" | "/")
        || base.query().is_some()
        || base.fragment().is_some()
    {
        return Err("invalid base URL");
    }
    Ok(base)
}

fn validate(config: OverlayConfig) -> io::Result<OverlayConfig> {
    if config.schema != 1 {
        return Err(invalid_data(format!(
            "unsupported config schema {}",
            config.schema
        )));
    }
    if config.game_id != "palworld" || config.user_id.trim().is_empty() {
        return Err(invalid_data("invalid game or user ID"));
    }
    if !matches!(config.scale, 0.8 | 1.0 | 1.25)
        || config.x.is_some() != config.y.is_some()
        || config.x.is_some_and(|value| !value.is_finite())
        || config.y.is_some_and(|value| !value.is_finite())
    {
        return Err(invalid_data("invalid overlay geometry"));
    }
    if config
        .display_id
        .as_ref()
        .is_some_and(|value| value.trim().is_empty())
    {
        return Err(invalid_data("invalid display ID"));
    }
    parse_base_url(&config.base_url).map_err(invalid_data)?;
    Ok(config)
}

fn decode(bytes: &[u8]) -> io::Result<OverlayConfig> {
    let mut value: Value =
        serde_json::from_slice(bytes).map_err(|error| invalid_data(error.to_string()))?;
    let object = value
        .as_object_mut()
        .ok_or_else(|| invalid_data("config must be an object"))?;
    match object.get("schema") {
        None => {
            object.insert("schema".into(), Value::from(1));
        }
        Some(Value::Number(number)) if number.as_u64() == Some(1) => {}
        Some(_) => return Err(invalid_data("unsupported config schema")),
    }
    let parsed = serde_json::from_value(value).map_err(|error| invalid_data(error.to_string()))?;
    validate(parsed)
}

pub fn load_from_path(config_dir: &Path) -> io::Result<Option<OverlayConfig>> {
    match fs::read(config_dir.join(CONFIG_FILE)) {
        Ok(bytes) => decode(&bytes).map(Some),
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
        Err(error) => Err(error),
    }
}

pub fn save_to_path(config_dir: &Path, config: &OverlayConfig) -> io::Result<()> {
    let config = validate(config.clone())?;
    fs::create_dir_all(config_dir)?;
    let temporary = config_dir.join(TEMP_FILE);
    let destination = config_dir.join(CONFIG_FILE);
    let result = (|| {
        let mut file = File::create(&temporary)?;
        serde_json::to_writer_pretty(&mut file, &config).map_err(io::Error::other)?;
        file.write_all(b"\n")?;
        file.sync_all()?;
        drop(file);
        fs::rename(&temporary, &destination)?;
        sync_directory(config_dir)?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(temporary);
    }
    result
}

#[cfg(unix)]
fn sync_directory(path: &Path) -> io::Result<()> {
    File::open(path)?.sync_all()
}

#[cfg(not(unix))]
fn sync_directory(_path: &Path) -> io::Result<()> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::{OverlayConfig, load_from_path, save_to_path};
    use std::fs;

    fn config() -> OverlayConfig {
        OverlayConfig {
            schema: 1,
            base_url: "https://palbox.test:8212".into(),
            game_id: "palworld".into(),
            user_id: "steam_42".into(),
            scale: 1.0,
            display_id: Some("display-1".into()),
            x: Some(12.5),
            y: Some(34.5),
            locked: true,
        }
    }

    fn temp_dir(name: &str) -> std::path::PathBuf {
        let path = std::env::temp_dir().join(format!(
            "palrest-overlay-{name}-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        fs::create_dir_all(&path).unwrap();
        path
    }

    #[test]
    fn round_trips_schema_one_config() {
        let dir = temp_dir("round-trip");
        save_to_path(&dir, &config()).unwrap();
        assert_eq!(load_from_path(&dir).unwrap(), Some(config()));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn migrates_unversioned_prerelease_config() {
        let dir = temp_dir("migration");
        fs::write(
            dir.join("config.json"),
            r#"{"baseUrl":"https://palbox.test","gameId":"palworld","userId":"uid","scale":0.8,"locked":false}"#,
        )
        .unwrap();
        let loaded = load_from_path(&dir).unwrap().unwrap();
        assert_eq!(loaded.schema, 1);
        assert_eq!(loaded.user_id, "uid");
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn rejects_unknown_future_schema() {
        let dir = temp_dir("future");
        fs::write(dir.join("config.json"), r#"{"schema":2}"#).unwrap();
        assert!(
            load_from_path(&dir)
                .unwrap_err()
                .to_string()
                .contains("schema")
        );
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn atomically_replaces_config_without_leaving_temp_file() {
        let dir = temp_dir("atomic");
        let mut updated = config();
        save_to_path(&dir, &config()).unwrap();
        updated.user_id = "replacement".into();
        save_to_path(&dir, &updated).unwrap();
        assert_eq!(load_from_path(&dir).unwrap(), Some(updated));
        assert!(!dir.join("config.json.tmp").exists());
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn rejects_base_urls_with_raw_dot_segment_paths() {
        let dir = temp_dir("dot-path");
        let mut invalid = config();
        invalid.base_url = "https://palbox.test/a/..".into();
        assert!(save_to_path(&dir, &invalid).is_err());
        fs::remove_dir_all(dir).unwrap();
    }
}
