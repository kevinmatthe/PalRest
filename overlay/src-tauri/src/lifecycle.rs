use std::time::Duration;

pub const SCAN_INTERVAL: Duration = Duration::from_secs(1);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LifecycleEvent {
    GameDetected(bool),
    EnterAdjustment,
    Lock,
    Quit,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct LifecycleState {
    pub game_running: bool,
    pub adjustment: bool,
    pub overlay_visible: bool,
    pub click_through: bool,
    pub quitting: bool,
}

impl Default for LifecycleState {
    fn default() -> Self {
        Self {
            game_running: false,
            adjustment: false,
            overlay_visible: false,
            click_through: true,
            quitting: false,
        }
    }
}

#[must_use]
pub fn reduce(mut state: LifecycleState, event: LifecycleEvent) -> LifecycleState {
    match event {
        LifecycleEvent::GameDetected(running) => {
            state.game_running = running;
            state.overlay_visible = running || state.adjustment;
        }
        LifecycleEvent::EnterAdjustment => {
            state.adjustment = true;
            state.overlay_visible = true;
            state.click_through = false;
        }
        LifecycleEvent::Lock => {
            state.adjustment = false;
            state.overlay_visible = state.game_running;
            state.click_through = true;
        }
        LifecycleEvent::Quit => state.quitting = true,
    }
    state
}

#[cfg(feature = "native")]
mod native {
    use super::{LifecycleEvent, LifecycleState, SCAN_INTERVAL, reduce};
    use crate::{config, process::ProcessMonitor};
    use std::sync::Mutex;
    use tauri::{AppHandle, Emitter, Manager};

    pub struct LifecycleController {
        state: Mutex<LifecycleState>,
    }

    impl Default for LifecycleController {
        fn default() -> Self {
            Self {
                state: Mutex::new(LifecycleState::default()),
            }
        }
    }

    impl LifecycleController {
        pub fn transition(&self, app: &AppHandle, event: LifecycleEvent) -> Result<(), String> {
            let mut state = self
                .state
                .lock()
                .map_err(|_| "lifecycle state is unavailable".to_string())?;
            let previous = *state;
            let next = reduce(previous, event);
            apply_transition(app, previous, next, event)?;
            *state = next;
            Ok(())
        }

        fn quitting(&self) -> bool {
            self.state.lock().map_or(true, |state| state.quitting)
        }
    }

    fn apply_transition(
        app: &AppHandle,
        previous: LifecycleState,
        next: LifecycleState,
        event: LifecycleEvent,
    ) -> Result<(), String> {
        let overlay = app
            .get_webview_window("overlay")
            .ok_or_else(|| "overlay window is unavailable".to_string())?;

        if previous.overlay_visible != next.overlay_visible {
            if next.overlay_visible {
                overlay.show().map_err(|error| error.to_string())?;
            } else {
                overlay.hide().map_err(|error| error.to_string())?;
            }
        }
        if previous.click_through != next.click_through {
            overlay
                .set_ignore_cursor_events(next.click_through)
                .map_err(|error| error.to_string())?;
        }

        match event {
            LifecycleEvent::EnterAdjustment => {
                overlay
                    .set_focusable(true)
                    .map_err(|error| error.to_string())?;
                overlay.set_focus().map_err(|error| error.to_string())?;
                overlay
                    .emit("adjustment-mode-changed", true)
                    .map_err(|error| error.to_string())?;
            }
            LifecycleEvent::Lock => {
                persist_geometry(app, &overlay)?;
                overlay
                    .set_focusable(false)
                    .map_err(|error| error.to_string())?;
                overlay
                    .emit("adjustment-mode-changed", false)
                    .map_err(|error| error.to_string())?;
            }
            LifecycleEvent::GameDetected(_) | LifecycleEvent::Quit => {}
        }
        Ok(())
    }

    fn persist_geometry(app: &AppHandle, overlay: &tauri::WebviewWindow) -> Result<(), String> {
        let config_dir = app
            .path()
            .app_config_dir()
            .map_err(|_| "could not resolve the application config directory".to_string())?;
        let Some(mut saved) =
            config::load_from_path(&config_dir).map_err(|error| error.to_string())?
        else {
            return Ok(());
        };
        let position = overlay
            .outer_position()
            .map_err(|error| error.to_string())?;
        saved.x = Some(f64::from(position.x));
        saved.y = Some(f64::from(position.y));
        saved.display_id = overlay
            .current_monitor()
            .map_err(|error| error.to_string())?
            .and_then(|monitor| monitor.name().map(ToOwned::to_owned));
        saved.locked = true;
        config::save_to_path(&config_dir, &saved).map_err(|error| error.to_string())
    }

    pub fn initialise(app: &AppHandle) -> Result<(), String> {
        let overlay = app
            .get_webview_window("overlay")
            .ok_or_else(|| "overlay window is unavailable".to_string())?;
        overlay
            .set_ignore_cursor_events(true)
            .map_err(|error| error.to_string())?;
        overlay
            .set_focusable(false)
            .map_err(|error| error.to_string())
    }

    pub fn start_monitor(app: AppHandle) {
        std::thread::spawn(move || {
            let mut processes = ProcessMonitor::default();
            let platform = if cfg!(target_os = "windows") {
                "windows"
            } else if cfg!(target_os = "macos") {
                "macos"
            } else {
                std::env::consts::OS
            };
            loop {
                let scan_started = std::time::Instant::now();
                let lifecycle = app.state::<LifecycleController>();
                if lifecycle.quitting() {
                    break;
                }
                let detected = processes.palworld_is_running(platform);
                let _ = lifecycle.transition(&app, LifecycleEvent::GameDetected(detected));
                std::thread::sleep(SCAN_INTERVAL.saturating_sub(scan_started.elapsed()));
            }
        });
    }
}

#[cfg(feature = "native")]
pub use native::{LifecycleController, initialise, start_monitor};

#[cfg(test)]
mod tests {
    use super::{LifecycleEvent, LifecycleState, SCAN_INTERVAL, reduce};
    use std::time::Duration;

    #[test]
    fn one_second_scans_meet_the_two_second_visibility_bound() {
        assert_eq!(SCAN_INTERVAL, Duration::from_secs(1));
        assert!(SCAN_INTERVAL * 2 <= Duration::from_secs(2));

        let shown = reduce(
            LifecycleState::default(),
            LifecycleEvent::GameDetected(true),
        );
        assert!(shown.overlay_visible);
        let hidden = reduce(shown, LifecycleEvent::GameDetected(false));
        assert!(!hidden.overlay_visible);
    }

    #[test]
    fn adjustment_enables_pointer_input_and_lock_restores_passthrough() {
        let adjusted = reduce(LifecycleState::default(), LifecycleEvent::EnterAdjustment);
        assert!(adjusted.adjustment);
        assert!(adjusted.overlay_visible);
        assert!(!adjusted.click_through);

        let locked = reduce(adjusted, LifecycleEvent::Lock);
        assert!(!locked.adjustment);
        assert!(locked.click_through);
    }

    #[test]
    fn absent_game_hides_only_the_overlay() {
        let state = reduce(
            LifecycleState {
                game_running: true,
                adjustment: false,
                overlay_visible: true,
                click_through: true,
                quitting: false,
            },
            LifecycleEvent::GameDetected(false),
        );
        assert!(!state.game_running);
        assert!(!state.overlay_visible);
        assert!(!state.adjustment);
        assert!(!state.quitting);
    }

    #[test]
    fn adjustment_remains_visible_when_a_scan_reports_no_game() {
        let adjusted = reduce(LifecycleState::default(), LifecycleEvent::EnterAdjustment);
        let scanned = reduce(adjusted, LifecycleEvent::GameDetected(false));
        assert!(scanned.adjustment);
        assert!(scanned.overlay_visible);
    }

    #[test]
    fn quit_is_explicit_state() {
        let state = reduce(LifecycleState::default(), LifecycleEvent::Quit);
        assert!(state.quitting);
    }
}
