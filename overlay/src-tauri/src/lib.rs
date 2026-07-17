pub mod config;
pub mod http;
pub mod lifecycle;
mod platform;
pub mod process;
pub mod tray;

#[cfg(any(feature = "native", test))]
fn config_error_is_recoverable(window_label: &str) -> bool {
    window_label == "settings"
}

#[cfg(test)]
mod tests {
    use super::config_error_is_recoverable;

    #[test]
    fn only_settings_can_recover_from_a_corrupt_config() {
        assert!(config_error_is_recoverable("settings"));
        assert!(!config_error_is_recoverable("overlay"));
        assert!(!config_error_is_recoverable("unknown"));
    }
}

#[cfg(feature = "native")]
mod native {
    use super::{config, http, lifecycle, platform, tray};
    use tauri::{AppHandle, Emitter, Manager, State, Window, WindowEvent};

    fn config_dir(app: &AppHandle) -> Result<std::path::PathBuf, String> {
        app.path()
            .app_config_dir()
            .map_err(|_| "could not resolve the application config directory".to_string())
    }

    #[tauri::command]
    fn load_config(
        app: AppHandle,
        window: Window,
    ) -> Result<Option<config::OverlayConfig>, String> {
        match config::load_from_path(&config_dir(&app)?) {
            Ok(config) => Ok(config),
            Err(_) if super::config_error_is_recoverable(window.label()) => Ok(None),
            Err(error) => Err(error.to_string()),
        }
    }

    #[tauri::command]
    fn save_config(app: AppHandle, config: config::OverlayConfig) -> Result<(), String> {
        let saved = config::save_editable_and_load_from_path(&config_dir(&app)?, &config)
            .map_err(|error| error.to_string())?;
        app.emit_to("overlay", "overlay-config-changed", &saved)
            .map_err(|error| error.to_string())
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

    #[tauri::command]
    fn detected_palworld_user_id() -> Option<String> {
        platform::detected_palworld_user_id()
    }

    #[tauri::command]
    fn set_adjustment_mode(app: AppHandle, enabled: bool) -> Result<(), String> {
        let event = if enabled {
            lifecycle::LifecycleEvent::EnterAdjustment
        } else {
            lifecycle::LifecycleEvent::Lock
        };
        app.state::<lifecycle::LifecycleController>()
            .transition(&app, event)
    }

    pub fn run() {
        let bridge = http::HttpBridge::new().expect("failed to create the restricted HTTP client");
        tauri::Builder::default()
            .manage(bridge)
            .manage(lifecycle::LifecycleController::default())
            .setup(|app| {
                let has_valid_config = app
                    .path()
                    .app_config_dir()
                    .ok()
                    .and_then(|path| config::load_from_path(&path).ok())
                    .flatten()
                    .is_some();
                lifecycle::initialise(app.handle()).map_err(std::io::Error::other)?;
                tray::setup(app, tray::should_show_settings_on_launch(has_valid_config))?;
                lifecycle::start_monitor(app.handle().clone());
                Ok(())
            })
            .on_window_event(|window, event| {
                if let WindowEvent::CloseRequested { api, .. } = event {
                    api.prevent_close();
                    if lifecycle::close_action(window.label()) == lifecycle::CloseAction::Hide {
                        let _ = window.hide();
                    }
                }
            })
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
