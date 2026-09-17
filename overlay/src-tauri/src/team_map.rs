//! The map webview exists only while requested; closing releases its renderer.
use tauri::{AppHandle, Manager, WebviewUrl, WebviewWindowBuilder};
use tauri_plugin_global_shortcut::{GlobalShortcutExt, ShortcutState};

pub const LABEL: &str = "team-map";
static WINDOW_OPERATION: std::sync::Mutex<()> = std::sync::Mutex::new(());

pub fn open(app: &AppHandle) -> Result<(), String> {
    let _guard = WINDOW_OPERATION.lock().map_err(|e| e.to_string())?;
    open_window(app)
}

fn open_window(app: &AppHandle) -> Result<(), String> {
    if let Some(window) = app.get_webview_window(LABEL) {
        window.show().map_err(|e| e.to_string())?;
        return window.set_focus().map_err(|e| e.to_string());
    }
    // Keep the opaque default: transparent() requires macos-private-api on macOS,
    // even when passed false.
    WebviewWindowBuilder::new(app, LABEL, WebviewUrl::App("index.html".into()))
        .title("PalREST · Online players")
        .inner_size(640.0, 560.0)
        .min_inner_size(400.0, 360.0)
        .resizable(true)
        .decorations(true)
        .always_on_top(true)
        .build()
        .map(|_| ())
        .map_err(|e| e.to_string())
}

pub fn close(app: &AppHandle) -> Result<(), String> {
    let _guard = WINDOW_OPERATION.lock().map_err(|e| e.to_string())?;
    close_window(app)
}

fn close_window(app: &AppHandle) -> Result<(), String> {
    if let Some(window) = app.get_webview_window(LABEL) {
        window.destroy().map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn toggle(app: &AppHandle) -> Result<(), String> {
    let _guard = WINDOW_OPERATION.lock().map_err(|e| e.to_string())?;
    if app.get_webview_window(LABEL).is_some() {
        close_window(app)
    } else {
        open_window(app)
    }
}

// Webview2 creation must run outside synchronous native event handlers.
pub fn request_toggle(app: &AppHandle) {
    let app = app.clone();
    tauri::async_runtime::spawn_blocking(move || {
        if let Err(error) = toggle(&app) {
            eprintln!("could not toggle team map: {error}");
        }
    });
}

pub fn register_shortcut(app: &AppHandle) {
    // Failure (e.g. another application owns this key) leaves the tray usable.
    if let Err(error) =
        app.global_shortcut()
            .on_shortcut("CommandOrControl+Shift+M", |app, _, event| {
                if event.state == ShortcutState::Pressed {
                    request_toggle(app);
                }
            })
    {
        eprintln!("team map shortcut unavailable; use the tray menu: {error}");
    }
}
