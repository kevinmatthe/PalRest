pub mod config;
pub mod http;

#[cfg(feature = "native")]
mod native {
    use super::{config, http};
    use tauri::{AppHandle, Manager, State, Window};

    fn config_dir(app: &AppHandle) -> Result<std::path::PathBuf, String> {
        app.path()
            .app_config_dir()
            .map_err(|_| "could not resolve the application config directory".to_string())
    }

    #[tauri::command]
    fn load_config(app: AppHandle) -> Result<Option<config::OverlayConfig>, String> {
        config::load_from_path(&config_dir(&app)?).map_err(|error| error.to_string())
    }

    #[tauri::command]
    fn save_config(app: AppHandle, config: config::OverlayConfig) -> Result<(), String> {
        config::save_to_path(&config_dir(&app)?, &config).map_err(|error| error.to_string())
    }

    #[tauri::command]
    async fn fetch_snapshot(
        bridge: State<'_, http::HttpBridge>,
        request: http::SnapshotRequest,
    ) -> Result<http::SnapshotResult, String> {
        bridge.fetch_snapshot(request).await
    }

    #[tauri::command]
    async fn list_players(
        bridge: State<'_, http::HttpBridge>,
        base_url: String,
    ) -> Result<Vec<http::PlayerListItem>, String> {
        bridge.list_players(base_url).await
    }

    #[tauri::command]
    fn current_window_label(window: Window) -> String {
        window.label().to_owned()
    }

    #[tauri::command]
    fn current_platform() -> &'static str {
        if cfg!(target_os = "windows") {
            "windows"
        } else if cfg!(target_os = "macos") {
            "macos"
        } else {
            std::env::consts::OS
        }
    }

    // Task 10 adds the platform-specific registry probe. Until then UID selection remains manual.
    #[tauri::command]
    fn detected_palworld_user_id() -> Option<String> {
        None
    }

    // Task 10 owns click-through and window positioning. Register the stable IPC surface now.
    #[tauri::command]
    fn set_adjustment_mode(_window: Window, _enabled: bool) {}

    pub fn run() {
        let bridge = http::HttpBridge::new().expect("failed to create the restricted HTTP client");
        tauri::Builder::default()
            .manage(bridge)
            .invoke_handler(tauri::generate_handler![
                load_config,
                save_config,
                fetch_snapshot,
                list_players,
                current_window_label,
                current_platform,
                detected_palworld_user_id,
                set_adjustment_mode
            ])
            .run(tauri::generate_context!())
            .expect("error while running PalREST Game Overlay");
    }
}

#[cfg(feature = "native")]
pub use native::run;
