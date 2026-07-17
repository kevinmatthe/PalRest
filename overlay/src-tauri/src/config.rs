use serde::{
    Deserialize, Deserializer, Serialize,
    de::{DeserializeSeed, Error as _, MapAccess, SeqAccess, Visitor},
};
use serde_json::Value;
#[cfg(test)]
use std::sync::Arc;
use std::{
    collections::BTreeMap,
    fmt,
    fs::{self, File, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::{
        Mutex,
        atomic::{AtomicU64, Ordering},
    },
};
use url::Url;

const CONFIG_FILE: &str = "config.json";

#[cfg(test)]
#[derive(Debug)]
enum TransactionStage {
    EditableLoaded,
    TempCreated(PathBuf),
}

#[cfg(test)]
type TransactionHook = Arc<dyn Fn(&TransactionStage) + Send + Sync>;

struct ConfigStore {
    transaction: Mutex<()>,
    temp_counter: AtomicU64,
    #[cfg(test)]
    test_hook: Mutex<Option<TransactionHook>>,
}

impl ConfigStore {
    const fn new() -> Self {
        Self {
            transaction: Mutex::new(()),
            temp_counter: AtomicU64::new(0),
            #[cfg(test)]
            test_hook: Mutex::new(None),
        }
    }

    #[cfg(test)]
    fn set_test_hook(&self, hook: TransactionHook) {
        *self.test_hook.lock().unwrap() = Some(hook);
    }

    #[cfg(test)]
    fn transaction_is_locked(&self) -> bool {
        matches!(
            self.transaction.try_lock(),
            Err(std::sync::TryLockError::WouldBlock)
        )
    }

    #[cfg(test)]
    fn notify(&self, stage: TransactionStage) {
        let hook = self.test_hook.lock().unwrap().clone();
        if let Some(hook) = hook {
            hook(&stage);
        }
    }
}

static CONFIG_STORE: ConfigStore = ConfigStore::new();

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SlotSelection {
    pub primary: String,
    pub fallback: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct LeftSelection {
    pub primary: String,
    pub fallback: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProgressSelection {
    pub mode: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub field: Option<String>,
}

#[derive(Default)]
enum ProgressField {
    #[default]
    Missing,
    Null,
    Value(String),
}

impl<'de> Deserialize<'de> for ProgressField {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        struct ProgressFieldVisitor;

        impl<'de> Visitor<'de> for ProgressFieldVisitor {
            type Value = ProgressField;

            fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str("a field ID string or null")
            }

            fn visit_unit<E>(self) -> Result<Self::Value, E> {
                Ok(ProgressField::Null)
            }

            fn visit_none<E>(self) -> Result<Self::Value, E> {
                Ok(ProgressField::Null)
            }

            fn visit_str<E>(self, value: &str) -> Result<Self::Value, E> {
                Ok(ProgressField::Value(value.to_owned()))
            }

            fn visit_string<E>(self, value: String) -> Result<Self::Value, E> {
                Ok(ProgressField::Value(value))
            }
        }

        deserializer.deserialize_any(ProgressFieldVisitor)
    }
}

impl<'de> Deserialize<'de> for ProgressSelection {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        #[derive(Deserialize)]
        #[serde(rename_all = "camelCase", deny_unknown_fields)]
        struct Wire {
            mode: String,
            #[serde(default)]
            field: ProgressField,
        }

        let wire = Wire::deserialize(deserializer)?;
        let field = match wire.field {
            ProgressField::Missing => None,
            ProgressField::Null => return Err(D::Error::custom("progress field cannot be null")),
            ProgressField::Value(value) => Some(value),
        };
        Ok(Self {
            mode: wire.mode,
            field,
        })
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct LayoutProfile {
    pub left: LeftSelection,
    pub slots: [SlotSelection; 4],
    pub progress: ProgressSelection,
}

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
    pub layouts: BTreeMap<String, LayoutProfile>,
}

#[derive(Debug, Clone, PartialEq)]
struct GeometryState {
    display_id: Option<String>,
    x: Option<f64>,
    y: Option<f64>,
    locked: bool,
}

impl GeometryState {
    fn capture(config: &OverlayConfig) -> Self {
        Self {
            display_id: config.display_id.clone(),
            x: config.x,
            y: config.y,
            locked: config.locked,
        }
    }

    fn apply_to(&self, config: &mut OverlayConfig) {
        config.display_id = self.display_id.clone();
        config.x = self.x;
        config.y = self.y;
        config.locked = self.locked;
    }
}

#[derive(Debug, Clone, PartialEq)]
pub struct GeometryRollbackToken {
    before: GeometryState,
    written: GeometryState,
}

pub(crate) fn palworld_default_layout() -> LayoutProfile {
    LayoutProfile {
        left: LeftSelection {
            primary: "map".into(),
            fallback: "player_badge".into(),
        },
        slots: [
            SlotSelection {
                primary: "network.latency".into(),
                fallback: "presence.last_online".into(),
            },
            SlotSelection {
                primary: "activity.today".into(),
                fallback: "activity.week".into(),
            },
            SlotSelection {
                primary: "policy.strategy".into(),
                fallback: "policy.enforcement".into(),
            },
            SlotSelection {
                primary: "policy.period_end".into(),
                fallback: "policy.remaining".into(),
            },
        ],
        progress: ProgressSelection {
            mode: "auto".into(),
            field: Some("policy.cycle_used".into()),
        },
    }
}

fn invalid_data(message: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, message.into())
}

fn unsupported(message: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::Unsupported, message.into())
}

fn is_safe_id(value: &str) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty()
        && bytes.len() <= 96
        && (bytes[0].is_ascii_lowercase() || bytes[0].is_ascii_digit())
        && bytes[1..].iter().all(|byte| {
            byte.is_ascii_lowercase() || byte.is_ascii_digit() || matches!(byte, b'.' | b'_' | b'-')
        })
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
    let slash = raw[authority_start..].find('/');
    let authority_end = slash
        .map(|index| authority_start + index)
        .unwrap_or(raw.len());
    let authority = &raw[authority_start..authority_end];
    let raw_suffix = &raw[authority_end..];
    if authority.contains('@') {
        return Err("invalid base URL");
    }
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
    if config.schema != 2 {
        return Err(unsupported(format!(
            "unsupported config schema {}",
            config.schema
        )));
    }
    if !is_safe_id(&config.game_id)
        || config.user_id.trim().is_empty()
        || !config.layouts.contains_key(&config.game_id)
    {
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
    for (game_id, layout) in &config.layouts {
        if !is_safe_id(game_id)
            || !matches!(layout.left.primary.as_str(), "map" | "player_badge")
            || !matches!(layout.left.fallback.as_str(), "map" | "player_badge")
            || layout.left.primary == layout.left.fallback
        {
            return Err(invalid_data("invalid layout identity selection"));
        }
        for slot in &layout.slots {
            if !is_safe_id(&slot.primary)
                || !is_safe_id(&slot.fallback)
                || slot.primary == slot.fallback
            {
                return Err(invalid_data("invalid layout slot"));
            }
        }
        let progress_valid = match layout.progress.mode.as_str() {
            "auto" => layout.progress.field.as_deref().is_none_or(is_safe_id),
            "field" => layout.progress.field.as_deref().is_some_and(is_safe_id),
            "hidden" => layout.progress.field.is_none(),
            _ => false,
        };
        if !progress_valid {
            return Err(invalid_data("invalid progress selection"));
        }
    }
    Ok(config)
}

struct StrictValueSeed;

impl<'de> DeserializeSeed<'de> for StrictValueSeed {
    type Value = Value;

    fn deserialize<D>(self, deserializer: D) -> Result<Self::Value, D::Error>
    where
        D: Deserializer<'de>,
    {
        deserializer.deserialize_any(StrictValueVisitor)
    }
}

struct StrictValueVisitor;

impl<'de> Visitor<'de> for StrictValueVisitor {
    type Value = Value;

    fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("a JSON value without duplicate object keys")
    }

    fn visit_bool<E>(self, value: bool) -> Result<Self::Value, E> {
        Ok(Value::Bool(value))
    }

    fn visit_i64<E>(self, value: i64) -> Result<Self::Value, E> {
        Ok(Value::Number(value.into()))
    }

    fn visit_u64<E>(self, value: u64) -> Result<Self::Value, E> {
        Ok(Value::Number(value.into()))
    }

    fn visit_f64<E>(self, value: f64) -> Result<Self::Value, E>
    where
        E: serde::de::Error,
    {
        serde_json::Number::from_f64(value)
            .map(Value::Number)
            .ok_or_else(|| E::custom("invalid JSON number"))
    }

    fn visit_str<E>(self, value: &str) -> Result<Self::Value, E> {
        Ok(Value::String(value.to_owned()))
    }

    fn visit_string<E>(self, value: String) -> Result<Self::Value, E> {
        Ok(Value::String(value))
    }

    fn visit_none<E>(self) -> Result<Self::Value, E> {
        Ok(Value::Null)
    }

    fn visit_unit<E>(self) -> Result<Self::Value, E> {
        Ok(Value::Null)
    }

    fn visit_seq<A>(self, mut sequence: A) -> Result<Self::Value, A::Error>
    where
        A: SeqAccess<'de>,
    {
        let mut values = Vec::new();
        while let Some(value) = sequence.next_element_seed(StrictValueSeed)? {
            values.push(value);
        }
        Ok(Value::Array(values))
    }

    fn visit_map<A>(self, mut map: A) -> Result<Self::Value, A::Error>
    where
        A: MapAccess<'de>,
    {
        let mut values = serde_json::Map::new();
        while let Some(key) = map.next_key::<String>()? {
            if values.contains_key(&key) {
                return Err(A::Error::custom(format!("duplicate JSON key {key}")));
            }
            values.insert(key, map.next_value_seed(StrictValueSeed)?);
        }
        Ok(Value::Object(values))
    }
}

fn decode_strict_value(bytes: &[u8]) -> io::Result<Value> {
    let mut deserializer = serde_json::Deserializer::from_slice(bytes);
    let value = StrictValueSeed
        .deserialize(&mut deserializer)
        .map_err(|error| {
            let message = error.to_string();
            if message.contains("duplicate JSON key ") {
                unsupported(message)
            } else {
                invalid_data(message)
            }
        })?;
    deserializer
        .end()
        .map_err(|error| invalid_data(error.to_string()))?;
    Ok(value)
}

fn decode(bytes: &[u8]) -> io::Result<OverlayConfig> {
    let mut value = decode_strict_value(bytes)?;
    let object = value
        .as_object_mut()
        .ok_or_else(|| invalid_data("config must be an object"))?;
    match object.get("schema") {
        None => {
            object.insert("schema".into(), Value::from(2));
            object.insert(
                "layouts".into(),
                serde_json::json!({ "palworld": palworld_default_layout() }),
            );
        }
        Some(Value::Number(number)) if number.as_u64() == Some(1) => {
            object.insert("schema".into(), Value::from(2));
            object.insert(
                "layouts".into(),
                serde_json::json!({ "palworld": palworld_default_layout() }),
            );
        }
        Some(Value::Number(number)) if number.as_u64() == Some(2) => {}
        Some(_) => return Err(unsupported("unsupported config schema")),
    }
    let parsed = serde_json::from_value(value).map_err(|error| invalid_data(error.to_string()))?;
    validate(parsed)
}

impl ConfigStore {
    fn lock(&self) -> io::Result<std::sync::MutexGuard<'_, ()>> {
        self.transaction
            .lock()
            .map_err(|_| io::Error::other("configuration transaction lock is poisoned"))
    }

    fn load_unlocked(&self, config_dir: &Path) -> io::Result<Option<OverlayConfig>> {
        match fs::read(config_dir.join(CONFIG_FILE)) {
            Ok(bytes) => decode(&bytes).map(Some),
            Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(error) => Err(error),
        }
    }

    fn load(&self, config_dir: &Path) -> io::Result<Option<OverlayConfig>> {
        let _transaction = self.lock()?;
        self.load_unlocked(config_dir)
    }

    fn create_temporary(&self, config_dir: &Path) -> io::Result<(PathBuf, File)> {
        loop {
            let sequence = self.temp_counter.fetch_add(1, Ordering::Relaxed);
            let path = config_dir.join(format!(
                "{CONFIG_FILE}.tmp.{}.{}",
                std::process::id(),
                sequence
            ));
            match OpenOptions::new().write(true).create_new(true).open(&path) {
                Ok(file) => return Ok((path, file)),
                Err(error) if error.kind() == io::ErrorKind::AlreadyExists => continue,
                Err(error) => return Err(error),
            }
        }
    }

    fn save_unlocked(&self, config_dir: &Path, config: &OverlayConfig) -> io::Result<()> {
        let config = validate(config.clone())?;
        fs::create_dir_all(config_dir)?;
        let (temporary, mut file) = self.create_temporary(config_dir)?;
        #[cfg(test)]
        self.notify(TransactionStage::TempCreated(temporary.clone()));
        let destination = config_dir.join(CONFIG_FILE);
        let result = (|| {
            serde_json::to_writer_pretty(&mut file, &config).map_err(io::Error::other)?;
            file.write_all(b"\n")?;
            file.sync_all()?;
            drop(file);
            fs::rename(&temporary, &destination)?;
            sync_directory(config_dir)?;
            Ok(())
        })();
        if result.is_err() {
            let _ = fs::remove_file(&temporary);
        }
        result
    }

    fn save(&self, config_dir: &Path, config: &OverlayConfig) -> io::Result<()> {
        let _transaction = self.lock()?;
        self.save_unlocked(config_dir, config)
    }

    fn merge_editable(
        &self,
        config_dir: &Path,
        incoming: &OverlayConfig,
    ) -> io::Result<OverlayConfig> {
        let merged = match self.load_unlocked(config_dir) {
            Ok(Some(mut current)) => {
                #[cfg(test)]
                self.notify(TransactionStage::EditableLoaded);
                current.base_url = incoming.base_url.clone();
                current.game_id = incoming.game_id.clone();
                current.user_id = incoming.user_id.clone();
                current.scale = incoming.scale;
                current.locked = incoming.locked;
                current.layouts = incoming.layouts.clone();
                current
            }
            Ok(None) => incoming.clone(),
            Err(error) if error.kind() == io::ErrorKind::InvalidData => incoming.clone(),
            Err(error) => return Err(error),
        };
        self.save_unlocked(config_dir, &merged)?;
        Ok(merged)
    }

    fn save_editable(&self, config_dir: &Path, incoming: &OverlayConfig) -> io::Result<()> {
        let _transaction = self.lock()?;
        self.merge_editable(config_dir, incoming).map(|_| ())
    }

    fn save_editable_and_load(
        &self,
        config_dir: &Path,
        incoming: &OverlayConfig,
    ) -> io::Result<OverlayConfig> {
        let _transaction = self.lock()?;
        let saved = self.merge_editable(config_dir, incoming)?;
        let loaded = self
            .load_unlocked(config_dir)?
            .ok_or_else(|| io::Error::new(io::ErrorKind::NotFound, "saved config was not found"))?;
        debug_assert_eq!(saved, loaded);
        Ok(loaded)
    }

    fn save_geometry(
        &self,
        config_dir: &Path,
        display_id: Option<String>,
        x: f64,
        y: f64,
    ) -> io::Result<Option<GeometryRollbackToken>> {
        let _transaction = self.lock()?;
        let Some(mut current) = self.load_unlocked(config_dir)? else {
            return Ok(None);
        };
        let before = GeometryState::capture(&current);
        current.display_id = display_id;
        current.x = Some(x);
        current.y = Some(y);
        current.locked = true;
        let written = GeometryState::capture(&current);
        self.save_unlocked(config_dir, &current)?;
        Ok(Some(GeometryRollbackToken { before, written }))
    }

    fn restore_geometry(
        &self,
        config_dir: &Path,
        rollback: Option<&GeometryRollbackToken>,
    ) -> io::Result<()> {
        let _transaction = self.lock()?;
        let Some(rollback) = rollback else {
            return Ok(());
        };
        let Some(mut current) = self.load_unlocked(config_dir)? else {
            return Ok(());
        };
        if GeometryState::capture(&current) == rollback.written {
            rollback.before.apply_to(&mut current);
            self.save_unlocked(config_dir, &current)?;
        }
        Ok(())
    }
}

pub fn load_from_path(config_dir: &Path) -> io::Result<Option<OverlayConfig>> {
    CONFIG_STORE.load(config_dir)
}

pub fn save_to_path(config_dir: &Path, config: &OverlayConfig) -> io::Result<()> {
    CONFIG_STORE.save(config_dir, config)
}

/// Saves fields controlled by Settings without allowing a stale renderer copy to overwrite
/// geometry most recently captured by the native window lifecycle.
pub fn save_editable_to_path(config_dir: &Path, incoming: &OverlayConfig) -> io::Result<()> {
    CONFIG_STORE.save_editable(config_dir, incoming)
}

pub fn save_editable_and_load_from_path(
    config_dir: &Path,
    incoming: &OverlayConfig,
) -> io::Result<OverlayConfig> {
    CONFIG_STORE.save_editable_and_load(config_dir, incoming)
}

pub fn save_geometry_to_path(
    config_dir: &Path,
    display_id: Option<String>,
    x: f64,
    y: f64,
) -> io::Result<Option<GeometryRollbackToken>> {
    CONFIG_STORE.save_geometry(config_dir, display_id, x, y)
}

pub fn restore_geometry_to_path(
    config_dir: &Path,
    rollback: Option<&GeometryRollbackToken>,
) -> io::Result<()> {
    CONFIG_STORE.restore_geometry(config_dir, rollback)
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
    use super::{
        ConfigStore, LayoutProfile, LeftSelection, OverlayConfig, ProgressSelection, SlotSelection,
        TransactionStage, load_from_path, restore_geometry_to_path,
        save_editable_and_load_from_path, save_editable_to_path, save_geometry_to_path,
        save_to_path,
    };
    use std::{
        collections::BTreeMap,
        fs,
        sync::{Arc, Barrier, Mutex},
        thread,
    };

    fn default_layout() -> LayoutProfile {
        LayoutProfile {
            left: LeftSelection {
                primary: "map".into(),
                fallback: "player_badge".into(),
            },
            slots: [
                SlotSelection {
                    primary: "network.latency".into(),
                    fallback: "presence.last_online".into(),
                },
                SlotSelection {
                    primary: "activity.today".into(),
                    fallback: "activity.week".into(),
                },
                SlotSelection {
                    primary: "policy.strategy".into(),
                    fallback: "policy.enforcement".into(),
                },
                SlotSelection {
                    primary: "policy.period_end".into(),
                    fallback: "policy.remaining".into(),
                },
            ],
            progress: ProgressSelection {
                mode: "auto".into(),
                field: Some("policy.cycle_used".into()),
            },
        }
    }

    fn custom_layout() -> LayoutProfile {
        LayoutProfile {
            left: LeftSelection {
                primary: "player_badge".into(),
                fallback: "map".into(),
            },
            slots: [
                SlotSelection {
                    primary: "custom.alpha".into(),
                    fallback: "custom.beta".into(),
                },
                SlotSelection {
                    primary: "network.latency".into(),
                    fallback: "presence.last_online".into(),
                },
                SlotSelection {
                    primary: "activity.week".into(),
                    fallback: "activity.today".into(),
                },
                SlotSelection {
                    primary: "policy.remaining".into(),
                    fallback: "policy.period_end".into(),
                },
            ],
            progress: ProgressSelection {
                mode: "field".into(),
                field: Some("custom.progress".into()),
            },
        }
    }

    fn config() -> OverlayConfig {
        OverlayConfig {
            schema: 2,
            base_url: "https://palbox.test:8212".into(),
            game_id: "palworld".into(),
            user_id: "steam_42".into(),
            scale: 1.0,
            display_id: Some("display-1".into()),
            x: Some(12.5),
            y: Some(34.5),
            locked: true,
            layouts: BTreeMap::from([("palworld".into(), default_layout())]),
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
    fn round_trips_schema_two_config() {
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
        assert_eq!(loaded.schema, 2);
        assert_eq!(loaded.user_id, "uid");
        assert_eq!(
            loaded.layouts,
            BTreeMap::from([("palworld".into(), default_layout())])
        );
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn migrates_schema_one_and_preserves_connection_uid_scale_lock_and_geometry() {
        let dir = temp_dir("schema-one-migration");
        fs::write(
            dir.join("config.json"),
            r#"{"schema":1,"baseUrl":"https://palbox.test:9443","gameId":"palworld","userId":" uid ","scale":1.25,"displayId":"screen-1","x":-12.5,"y":8.0,"locked":false}"#,
        ).unwrap();
        let loaded = load_from_path(&dir).unwrap().unwrap();
        assert_eq!(loaded.schema, 2);
        assert_eq!(loaded.base_url, "https://palbox.test:9443");
        assert_eq!(loaded.user_id, " uid ");
        assert_eq!(loaded.scale, 1.25);
        assert_eq!(loaded.display_id.as_deref(), Some("screen-1"));
        assert_eq!((loaded.x, loaded.y), (Some(-12.5), Some(8.0)));
        assert!(!loaded.locked);
        assert_eq!(loaded.layouts["palworld"], default_layout());
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn rejects_unknown_future_schema() {
        let dir = temp_dir("future");
        fs::write(dir.join("config.json"), r#"{"schema":3}"#).unwrap();
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

    #[test]
    fn rejects_any_raw_authority_userinfo_marker() {
        for base_url in ["https://user@palbox.test", "https://@palbox.test"] {
            let dir = temp_dir("userinfo");
            let mut invalid = config();
            invalid.base_url = base_url.into();
            assert!(save_to_path(&dir, &invalid).is_err(), "accepted {base_url}");
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn editable_save_preserves_newer_native_geometry() {
        let dir = temp_dir("editable-merge");
        let native = config();
        save_to_path(&dir, &native).unwrap();
        let mut incoming = native.clone();
        incoming.base_url = "https://replacement.test".into();
        incoming.user_id = "replacement".into();
        incoming.scale = 1.25;
        incoming.display_id = Some("stale-display".into());
        incoming.x = Some(999.0);
        incoming.y = Some(999.0);
        incoming.locked = false;
        incoming.layouts.insert("palworld".into(), custom_layout());
        save_editable_to_path(&dir, &incoming).unwrap();
        let merged = load_from_path(&dir).unwrap().unwrap();
        assert_eq!(merged.base_url, incoming.base_url);
        assert_eq!(merged.user_id, incoming.user_id);
        assert_eq!(merged.scale, 1.25);
        assert_eq!(merged.display_id, native.display_id);
        assert_eq!(merged.x, native.x);
        assert_eq!(merged.y, native.y);
        assert_eq!(merged.layouts, incoming.layouts);
        assert!(!merged.locked);
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn editable_save_returns_the_canonical_config_with_native_geometry() {
        let dir = temp_dir("editable-return");
        let native = config();
        save_to_path(&dir, &native).unwrap();
        let mut incoming = native.clone();
        incoming.base_url = "https://replacement.test".into();
        incoming.user_id = "replacement".into();
        incoming.display_id = Some("stale-display".into());
        incoming.x = Some(999.0);
        incoming.y = Some(999.0);

        let saved = save_editable_and_load_from_path(&dir, &incoming).unwrap();

        assert_eq!(saved.base_url, incoming.base_url);
        assert_eq!(saved.user_id, incoming.user_id);
        assert_eq!(saved.display_id, native.display_id);
        assert_eq!(saved.x, native.x);
        assert_eq!(saved.y, native.y);
        assert_eq!(load_from_path(&dir).unwrap(), Some(saved));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn legacy_fixed_temp_path_cannot_block_an_editable_save() {
        let dir = temp_dir("editable-legacy-temp");
        let original = config();
        save_to_path(&dir, &original).unwrap();
        fs::create_dir(dir.join("config.json.tmp")).unwrap();
        let mut incoming = original.clone();
        incoming.user_id = "replacement".into();

        assert_eq!(
            save_editable_and_load_from_path(&dir, &incoming).unwrap(),
            incoming
        );
        assert_eq!(load_from_path(&dir).unwrap(), Some(incoming));
        assert!(dir.join("config.json.tmp").is_dir());
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn restores_old_geometry_after_a_later_window_failure() {
        let dir = temp_dir("geometry-rollback");
        let original = config();
        save_to_path(&dir, &original).unwrap();
        let backup = save_geometry_to_path(&dir, Some("new-display".into()), 80.0, 90.0).unwrap();
        assert_eq!(load_from_path(&dir).unwrap().unwrap().x, Some(80.0));
        restore_geometry_to_path(&dir, backup.as_ref()).unwrap();
        assert_eq!(load_from_path(&dir).unwrap(), Some(original));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn geometry_rollback_preserves_settings_saved_after_geometry_persist() {
        let dir = temp_dir("geometry-settings-interleave");
        let original = config();
        save_to_path(&dir, &original).unwrap();
        let rollback =
            save_geometry_to_path(&dir, Some("persisted-display".into()), 80.0, 90.0).unwrap();

        let mut settings = original.clone();
        settings.base_url = "https://settings-b.test".into();
        settings.user_id = "settings-b".into();
        settings.layouts.insert("palworld".into(), custom_layout());
        save_editable_to_path(&dir, &settings).unwrap();
        restore_geometry_to_path(&dir, rollback.as_ref()).unwrap();

        let restored = load_from_path(&dir).unwrap().unwrap();
        assert_eq!(restored.base_url, settings.base_url);
        assert_eq!(restored.user_id, settings.user_id);
        assert_eq!(restored.layouts, settings.layouts);
        assert_eq!(restored.display_id, original.display_id);
        assert_eq!((restored.x, restored.y), (original.x, original.y));
        assert_eq!(restored.locked, original.locked);
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn stale_geometry_rollback_does_not_overwrite_later_geometry() {
        let dir = temp_dir("geometry-cas");
        save_to_path(&dir, &config()).unwrap();
        let stale = save_geometry_to_path(&dir, Some("display-b".into()), 80.0, 90.0).unwrap();
        save_geometry_to_path(&dir, Some("display-c".into()), 180.0, 190.0).unwrap();

        restore_geometry_to_path(&dir, stale.as_ref()).unwrap();

        let current = load_from_path(&dir).unwrap().unwrap();
        assert_eq!(current.display_id.as_deref(), Some("display-c"));
        assert_eq!((current.x, current.y), (Some(180.0), Some(190.0)));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn shared_transaction_serializes_settings_and_geometry_read_modify_write() {
        let dir = temp_dir("serialized-rmw");
        let store = Arc::new(ConfigStore::new());
        store.save(&dir, &config()).unwrap();
        let editable_loaded = Arc::new(Barrier::new(2));
        let release_editable = Arc::new(Barrier::new(2));
        store.set_test_hook({
            let editable_loaded = Arc::clone(&editable_loaded);
            let release_editable = Arc::clone(&release_editable);
            Arc::new(move |stage| {
                if matches!(stage, TransactionStage::EditableLoaded)
                    && thread::current().name() == Some("settings-writer")
                {
                    editable_loaded.wait();
                    release_editable.wait();
                }
            })
        });

        let settings_store = Arc::clone(&store);
        let settings_dir = dir.clone();
        let mut settings = config();
        settings.base_url = "https://settings.test".into();
        settings.layouts.insert("palworld".into(), custom_layout());
        let settings_writer = thread::Builder::new()
            .name("settings-writer".into())
            .spawn(move || {
                settings_store
                    .save_editable(&settings_dir, &settings)
                    .unwrap()
            })
            .unwrap();
        editable_loaded.wait();
        assert!(store.transaction_is_locked());

        let geometry_started = Arc::new(Barrier::new(2));
        let geometry_store = Arc::clone(&store);
        let geometry_dir = dir.clone();
        let geometry_writer = thread::Builder::new()
            .name("geometry-writer".into())
            .spawn({
                let geometry_started = Arc::clone(&geometry_started);
                move || {
                    geometry_started.wait();
                    geometry_store
                        .save_geometry(&geometry_dir, Some("display-b".into()), 80.0, 90.0)
                        .unwrap();
                }
            })
            .unwrap();
        geometry_started.wait();
        release_editable.wait();
        settings_writer.join().unwrap();
        geometry_writer.join().unwrap();

        let saved = store.load(&dir).unwrap().unwrap();
        assert_eq!(saved.base_url, "https://settings.test");
        assert_eq!(saved.layouts["palworld"], custom_layout());
        assert_eq!(saved.display_id.as_deref(), Some("display-b"));
        assert_eq!((saved.x, saved.y), (Some(80.0), Some(90.0)));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn atomic_writers_use_distinct_owned_temp_files() {
        let dir = temp_dir("unique-temp");
        let store = Arc::new(ConfigStore::new());
        let paths = Arc::new(Mutex::new(Vec::new()));
        store.set_test_hook({
            let paths = Arc::clone(&paths);
            Arc::new(move |stage| {
                if let TransactionStage::TempCreated(path) = stage {
                    paths.lock().unwrap().push(path.clone());
                }
            })
        });
        let start = Arc::new(Barrier::new(3));
        let writers = ["first-writer", "second-writer"].map(|user_id| {
            let store = Arc::clone(&store);
            let dir = dir.clone();
            let start = Arc::clone(&start);
            thread::spawn(move || {
                let mut config = config();
                config.user_id = user_id.into();
                start.wait();
                store.save(&dir, &config).unwrap();
            })
        });
        start.wait();
        for writer in writers {
            writer.join().unwrap();
        }

        let paths = paths.lock().unwrap();
        assert_eq!(paths.len(), 2);
        assert_ne!(paths[0], paths[1]);
        assert!(paths.iter().all(|path| !path.exists()));
        assert!(matches!(
            store.load(&dir).unwrap().unwrap().user_id.as_str(),
            "first-writer" | "second-writer"
        ));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn geometry_only_save_preserves_layouts() {
        let dir = temp_dir("geometry-layout");
        let mut original = config();
        original.layouts.insert("palworld".into(), custom_layout());
        save_to_path(&dir, &original).unwrap();
        save_geometry_to_path(&dir, Some("new-display".into()), 80.0, 90.0).unwrap();
        assert_eq!(
            load_from_path(&dir).unwrap().unwrap().layouts,
            original.layouts
        );
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn schema_two_round_trip_preserves_unknown_safe_ids() {
        let dir = temp_dir("schema-two-custom");
        let mut value = config();
        value.game_id = "custom-game".into();
        value.layouts = BTreeMap::from([("custom-game".into(), custom_layout())]);
        save_to_path(&dir, &value).unwrap();
        assert_eq!(load_from_path(&dir).unwrap(), Some(value));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn rejects_invalid_layout_shapes_and_ids() {
        let cases = [
            r#"{"schema":2,"baseUrl":"https://palbox.test","gameId":"Bad Game","userId":"uid","scale":1,"locked":true,"layouts":{}}"#,
            r#"{"schema":2,"baseUrl":"https://palbox.test","gameId":"palworld","userId":"uid","scale":1,"locked":true,"layouts":{"Bad Game":{"left":{"primary":"map","fallback":"player_badge"},"slots":[],"progress":{"mode":"hidden"}}}}"#,
            r#"{"schema":2,"baseUrl":"https://palbox.test","gameId":"palworld","userId":"uid","scale":1,"locked":true,"layouts":{"palworld":{"left":{"primary":"map","fallback":"map"},"slots":[{"primary":"a","fallback":"b"},{"primary":"c","fallback":"d"},{"primary":"e","fallback":"f"},{"primary":"g","fallback":"h"}],"progress":{"mode":"hidden"}}}}"#,
            r#"{"schema":2,"baseUrl":"https://palbox.test","gameId":"palworld","userId":"uid","scale":1,"locked":true,"layouts":{"palworld":{"left":{"primary":"map","fallback":"player_badge"},"slots":[{"primary":"same","fallback":"same"},{"primary":"c","fallback":"d"},{"primary":"e","fallback":"f"},{"primary":"g","fallback":"h"}],"progress":{"mode":"field"}}}}"#,
        ];
        for (index, bytes) in cases.iter().enumerate() {
            let dir = temp_dir(&format!("invalid-layout-{index}"));
            fs::write(dir.join("config.json"), bytes).unwrap();
            assert!(load_from_path(&dir).is_err(), "accepted case {index}");
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn safe_ids_enforce_ascii_96_byte_boundary() {
        let dir = temp_dir("id-boundary");
        let mut accepted = config();
        accepted.layouts.get_mut("palworld").unwrap().slots[0].primary =
            format!("a{}", "b".repeat(95));
        save_to_path(&dir, &accepted).unwrap();

        for invalid_id in [format!("a{}", "b".repeat(96)), "字段.safe".into()] {
            let mut rejected = accepted.clone();
            rejected.layouts.get_mut("palworld").unwrap().slots[0].primary = invalid_id;
            assert!(save_to_path(&dir, &rejected).is_err());
        }
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn rejects_duplicate_json_keys_at_every_depth_and_preserves_future_bytes() {
        let valid = serde_json::to_string(&config()).unwrap();
        let future = valid.replacen(r#""schema":2"#, r#""schema":3"#, 1);
        let cases = [
            valid.replacen(r#""schema":2"#, r#""schema":3,"schema":2"#, 1),
            valid.replacen(r#""schema":2"#, r#""schema":2,"schema":3"#, 1),
            valid.replacen(
                r#""primary":"network.latency""#,
                r#""primary":"duplicate.first","primary":"network.latency""#,
                1,
            ),
            valid.replacen(
                r#""palworld":{"left""#,
                r#""palworld":{"left":{"primary":"map","fallback":"player_badge"},"left""#,
                1,
            ),
            future.replacen(r#""locked":true"#, r#""locked":false,"locked":true"#, 1),
            future.replacen(
                r#""primary":"network.latency""#,
                r#""primary":"duplicate.first","primary":"network.latency""#,
                1,
            ),
        ];
        for (index, bytes) in cases.into_iter().enumerate() {
            let dir = temp_dir(&format!("duplicate-json-{index}"));
            fs::write(dir.join("config.json"), &bytes).unwrap();
            assert!(
                load_from_path(&dir).is_err(),
                "accepted duplicate case {index}"
            );
            assert!(save_editable_to_path(&dir, &config()).is_err());
            assert_eq!(fs::read_to_string(dir.join("config.json")).unwrap(), bytes);
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn rejects_explicit_null_progress_field_but_accepts_missing_auto_field() {
        let dir = temp_dir("progress-null");
        let valid = serde_json::to_string(&config()).unwrap();
        let explicit_null = valid.replacen(r#""field":"policy.cycle_used""#, r#""field":null"#, 1);
        fs::write(dir.join("config.json"), explicit_null).unwrap();
        assert!(load_from_path(&dir).is_err());

        let missing = valid.replacen(",\"field\":\"policy.cycle_used\"", "", 1);
        fs::write(dir.join("config.json"), missing).unwrap();
        assert!(load_from_path(&dir).is_ok());
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn capability_allows_only_required_event_and_drag_operations() {
        let capability: serde_json::Value =
            serde_json::from_str(include_str!("../capabilities/default.json")).unwrap();
        assert_eq!(
            capability["permissions"],
            serde_json::json!([
                "core:event:allow-listen",
                "core:event:allow-unlisten",
                "core:window:allow-start-dragging"
            ])
        );
    }

    #[test]
    fn first_editable_save_accepts_initial_geometry() {
        let dir = temp_dir("editable-first");
        save_editable_to_path(&dir, &config()).unwrap();
        assert_eq!(load_from_path(&dir).unwrap(), Some(config()));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn editable_save_replaces_a_malformed_config() {
        let dir = temp_dir("editable-repair");
        fs::write(dir.join("config.json"), b"{broken json").unwrap();

        save_editable_to_path(&dir, &config()).unwrap();

        assert_eq!(load_from_path(&dir).unwrap(), Some(config()));
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn editable_save_never_overwrites_a_future_schema() {
        let dir = temp_dir("editable-future");
        let future = br#"{"schema":3,"future":"value"}"#;
        fs::write(dir.join("config.json"), future).unwrap();

        assert!(save_editable_to_path(&dir, &config()).is_err());

        assert_eq!(fs::read(dir.join("config.json")).unwrap(), future);
        fs::remove_dir_all(dir).unwrap();
    }
}
