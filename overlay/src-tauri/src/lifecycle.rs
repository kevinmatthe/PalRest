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

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum LifecycleEffect {
    PersistGeometry,
    RestorePosition(i32, i32),
    Show,
    Hide,
    SetClickThrough(bool),
    SetFocusable(bool),
    Focus,
    EmitAdjustment(bool),
}

#[must_use]
pub fn initial_window_effects(
    x: Option<f64>,
    y: Option<f64>,
    locked: bool,
) -> Vec<LifecycleEffect> {
    let mut effects = Vec::with_capacity(3);
    if let (Some(x), Some(y)) = (x, y) {
        effects.push(LifecycleEffect::RestorePosition(
            x.round() as i32,
            y.round() as i32,
        ));
    }
    effects.push(LifecycleEffect::SetFocusable(!locked));
    effects.push(LifecycleEffect::SetClickThrough(locked));
    effects
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TransitionPlan {
    pub previous: LifecycleState,
    pub next: LifecycleState,
    pub effects: Vec<LifecycleEffect>,
}

#[must_use]
pub fn plan_transition(
    previous: LifecycleState,
    event: LifecycleEvent,
    platform: &str,
) -> TransitionPlan {
    let next = reduce(previous, event);
    let mut effects = Vec::new();
    if previous == next {
        return TransitionPlan {
            previous,
            next,
            effects,
        };
    }
    match event {
        LifecycleEvent::GameDetected(_) => {
            if previous.overlay_visible != next.overlay_visible {
                effects.push(if next.overlay_visible {
                    LifecycleEffect::Show
                } else {
                    LifecycleEffect::Hide
                });
            }
        }
        LifecycleEvent::EnterAdjustment => {
            if previous.overlay_visible != next.overlay_visible {
                effects.push(LifecycleEffect::Show);
            }
            if previous.click_through != next.click_through {
                effects.push(LifecycleEffect::SetClickThrough(next.click_through));
            }
            effects.extend([
                LifecycleEffect::SetFocusable(true),
                LifecycleEffect::Focus,
                LifecycleEffect::EmitAdjustment(true),
            ]);
        }
        LifecycleEvent::Lock => {
            effects.push(LifecycleEffect::PersistGeometry);
            if previous.click_through != next.click_through {
                effects.push(LifecycleEffect::SetClickThrough(next.click_through));
            }
            if platform == "macos" && next.overlay_visible {
                effects.extend([
                    LifecycleEffect::Hide,
                    LifecycleEffect::SetFocusable(false),
                    LifecycleEffect::Show,
                ]);
            } else {
                if previous.overlay_visible != next.overlay_visible {
                    effects.push(if next.overlay_visible {
                        LifecycleEffect::Show
                    } else {
                        LifecycleEffect::Hide
                    });
                }
                effects.push(LifecycleEffect::SetFocusable(false));
            }
            effects.push(LifecycleEffect::EmitAdjustment(false));
        }
        LifecycleEvent::Quit => {}
    }
    TransitionPlan {
        previous,
        next,
        effects,
    }
}

pub trait LifecycleEffectExecutor {
    fn apply(&mut self, effect: LifecycleEffect) -> Result<(), String>;
    fn rollback(&mut self, effect: LifecycleEffect);
}

pub fn execute_effects(
    executor: &mut impl LifecycleEffectExecutor,
    effects: &[LifecycleEffect],
) -> Result<(), String> {
    let mut applied = Vec::with_capacity(effects.len());
    for &effect in effects {
        if let Err(error) = executor.apply(effect) {
            for &completed in applied.iter().rev() {
                executor.rollback(completed);
            }
            return Err(error);
        }
        applied.push(effect);
    }
    Ok(())
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CloseAction {
    Keep,
    Hide,
}

#[must_use]
pub fn close_action(label: &str) -> CloseAction {
    if label == "overlay" {
        CloseAction::Keep
    } else {
        CloseAction::Hide
    }
}

#[cfg(feature = "native")]
mod native {
    use super::{
        LifecycleEffect, LifecycleEffectExecutor, LifecycleEvent, LifecycleState, SCAN_INTERVAL,
        execute_effects, initial_window_effects, plan_transition,
    };
    use crate::{config, process::ProcessMonitor};
    use std::sync::Mutex;
    use tauri::{AppHandle, Emitter, Manager};

    pub struct LifecycleController {
        state: Mutex<LifecycleState>,
        operation: Mutex<()>,
    }

    impl Default for LifecycleController {
        fn default() -> Self {
            Self {
                state: Mutex::new(LifecycleState::default()),
                operation: Mutex::new(()),
            }
        }
    }

    impl LifecycleController {
        pub fn transition(&self, app: &AppHandle, event: LifecycleEvent) -> Result<(), String> {
            let _operation = self
                .operation
                .lock()
                .map_err(|_| "lifecycle transition is unavailable".to_string())?;
            let previous = *self
                .state
                .lock()
                .map_err(|_| "lifecycle state is unavailable".to_string())?;
            let plan = plan_transition(previous, event, current_platform());
            if plan.effects.is_empty() {
                *self
                    .state
                    .lock()
                    .map_err(|_| "lifecycle state is unavailable".to_string())? = plan.next;
                return Ok(());
            }
            let overlay = app
                .get_webview_window("overlay")
                .ok_or_else(|| "overlay window is unavailable".to_string())?;
            execute_effects(
                &mut NativeExecutor {
                    app,
                    overlay,
                    geometry_backup: None,
                },
                &plan.effects,
            )?;
            let mut state = self
                .state
                .lock()
                .map_err(|_| "lifecycle state is unavailable".to_string())?;
            let next = plan.next;
            *state = next;
            Ok(())
        }

        fn quitting(&self) -> bool {
            self.state.lock().map_or(true, |state| state.quitting)
        }

        fn set_initial_locked(&self, locked: bool) -> Result<(), String> {
            self.state
                .lock()
                .map_err(|_| "lifecycle state is unavailable".to_string())?
                .click_through = locked;
            Ok(())
        }
    }

    struct NativeExecutor<'a> {
        app: &'a AppHandle,
        overlay: tauri::WebviewWindow,
        geometry_backup: Option<(std::path::PathBuf, Option<config::OverlayConfig>)>,
    }

    impl LifecycleEffectExecutor for NativeExecutor<'_> {
        fn apply(&mut self, effect: LifecycleEffect) -> Result<(), String> {
            let result = match effect {
                LifecycleEffect::PersistGeometry => return self.persist_geometry(),
                LifecycleEffect::RestorePosition(x, y) => self
                    .overlay
                    .set_position(tauri::PhysicalPosition::new(x, y)),
                LifecycleEffect::Show => self.overlay.show(),
                LifecycleEffect::Hide => self.overlay.hide(),
                LifecycleEffect::SetClickThrough(value) => {
                    self.overlay.set_ignore_cursor_events(value)
                }
                LifecycleEffect::SetFocusable(value) => self.overlay.set_focusable(value),
                LifecycleEffect::Focus => self.overlay.set_focus(),
                LifecycleEffect::EmitAdjustment(value) => {
                    self.overlay.emit("adjustment-mode-changed", value)
                }
            };
            result.map_err(|error| error.to_string())
        }

        fn rollback(&mut self, effect: LifecycleEffect) {
            match effect {
                LifecycleEffect::PersistGeometry => {
                    if let Some((dir, backup)) = &self.geometry_backup {
                        let _ = config::restore_geometry_to_path(dir, backup.as_ref());
                    }
                }
                LifecycleEffect::RestorePosition(_, _) => {}
                LifecycleEffect::Show => {
                    let _ = self.overlay.hide();
                }
                LifecycleEffect::Hide => {
                    let _ = self.overlay.show();
                }
                LifecycleEffect::SetClickThrough(value) => {
                    let _ = self.overlay.set_ignore_cursor_events(!value);
                }
                LifecycleEffect::SetFocusable(value) => {
                    let _ = self.overlay.set_focusable(!value);
                }
                LifecycleEffect::Focus => {
                    let _ = self.overlay.set_focusable(false);
                }
                LifecycleEffect::EmitAdjustment(value) => {
                    let _ = self.overlay.emit("adjustment-mode-changed", !value);
                }
            }
        }
    }

    impl NativeExecutor<'_> {
        fn persist_geometry(&mut self) -> Result<(), String> {
            let config_dir =
                self.app.path().app_config_dir().map_err(|_| {
                    "could not resolve the application config directory".to_string()
                })?;
            let position = self
                .overlay
                .outer_position()
                .map_err(|error| error.to_string())?;
            let display_id = self
                .overlay
                .current_monitor()
                .map_err(|error| error.to_string())?
                .and_then(|monitor| monitor.name().map(ToOwned::to_owned));
            let backup = config::save_geometry_to_path(
                &config_dir,
                display_id,
                f64::from(position.x),
                f64::from(position.y),
            )
            .map_err(|error| error.to_string())?;
            self.geometry_backup = Some((config_dir, backup));
            Ok(())
        }
    }

    fn current_platform() -> &'static str {
        if cfg!(target_os = "windows") {
            "windows"
        } else if cfg!(target_os = "macos") {
            "macos"
        } else {
            std::env::consts::OS
        }
    }

    pub fn initialise(app: &AppHandle) -> Result<(), String> {
        let overlay = app
            .get_webview_window("overlay")
            .ok_or_else(|| "overlay window is unavailable".to_string())?;
        let saved = app
            .path()
            .app_config_dir()
            .ok()
            .and_then(|dir| config::load_from_path(&dir).ok().flatten());
        let (x, y, locked) = saved.as_ref().map_or((None, None, true), |config| {
            (config.x, config.y, config.locked)
        });
        execute_effects(
            &mut NativeExecutor {
                app,
                overlay,
                geometry_backup: None,
            },
            &initial_window_effects(x, y, locked),
        )?;
        app.state::<LifecycleController>()
            .set_initial_locked(locked)
    }

    pub fn start_monitor(app: AppHandle) {
        std::thread::spawn(move || {
            let mut processes = ProcessMonitor::default();
            let platform = current_platform();
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
    use super::{
        CloseAction, LifecycleEffect, LifecycleEffectExecutor, LifecycleEvent, LifecycleState,
        SCAN_INTERVAL, close_action, execute_effects, initial_window_effects, plan_transition,
        reduce,
    };
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

    #[test]
    fn repeated_commands_have_no_effects() {
        let adjusted = reduce(LifecycleState::default(), LifecycleEvent::EnterAdjustment);
        assert!(
            plan_transition(adjusted, LifecycleEvent::EnterAdjustment, "windows")
                .effects
                .is_empty()
        );
        let locked = reduce(adjusted, LifecycleEvent::Lock);
        assert!(
            plan_transition(locked, LifecycleEvent::Lock, "windows")
                .effects
                .is_empty()
        );
    }

    #[test]
    fn mac_lock_persists_before_window_changes_and_reliably_clears_focus() {
        let adjusted = LifecycleState {
            game_running: true,
            adjustment: true,
            overlay_visible: true,
            click_through: false,
            quitting: false,
        };
        assert_eq!(
            plan_transition(adjusted, LifecycleEvent::Lock, "macos").effects,
            vec![
                LifecycleEffect::PersistGeometry,
                LifecycleEffect::SetClickThrough(true),
                LifecycleEffect::Hide,
                LifecycleEffect::SetFocusable(false),
                LifecycleEffect::Show,
                LifecycleEffect::EmitAdjustment(false),
            ]
        );
    }

    #[test]
    fn overlay_close_is_kept_in_sync_while_settings_close_hides() {
        assert_eq!(close_action("overlay"), CloseAction::Keep);
        assert_eq!(close_action("settings"), CloseAction::Hide);
    }

    #[test]
    fn geometry_failure_has_no_window_effects_and_the_same_plan_can_retry() {
        struct Fixture {
            fail_geometry_once: bool,
            applied: Vec<LifecycleEffect>,
        }
        impl LifecycleEffectExecutor for Fixture {
            fn apply(&mut self, effect: LifecycleEffect) -> Result<(), String> {
                self.applied.push(effect);
                if effect == LifecycleEffect::PersistGeometry && self.fail_geometry_once {
                    self.fail_geometry_once = false;
                    return Err("disk unavailable".into());
                }
                Ok(())
            }
            fn rollback(&mut self, _effect: LifecycleEffect) {}
        }
        let adjusted = LifecycleState {
            game_running: true,
            adjustment: true,
            overlay_visible: true,
            click_through: false,
            quitting: false,
        };
        let plan = plan_transition(adjusted, LifecycleEvent::Lock, "windows");
        let mut fixture = Fixture {
            fail_geometry_once: true,
            applied: vec![],
        };
        assert!(execute_effects(&mut fixture, &plan.effects).is_err());
        assert_eq!(fixture.applied, vec![LifecycleEffect::PersistGeometry]);
        fixture.applied.clear();
        assert!(execute_effects(&mut fixture, &plan.effects).is_ok());
        assert_eq!(fixture.applied, plan.effects);
    }

    #[test]
    fn window_failure_rolls_back_completed_effects_in_reverse_order() {
        struct Fixture {
            rolled_back: Vec<LifecycleEffect>,
        }
        impl LifecycleEffectExecutor for Fixture {
            fn apply(&mut self, effect: LifecycleEffect) -> Result<(), String> {
                if effect == LifecycleEffect::Hide {
                    Err("window unavailable".into())
                } else {
                    Ok(())
                }
            }
            fn rollback(&mut self, effect: LifecycleEffect) {
                self.rolled_back.push(effect);
            }
        }
        let effects = [
            LifecycleEffect::PersistGeometry,
            LifecycleEffect::SetClickThrough(true),
            LifecycleEffect::Hide,
        ];
        let mut fixture = Fixture {
            rolled_back: vec![],
        };
        assert!(execute_effects(&mut fixture, &effects).is_err());
        assert_eq!(
            fixture.rolled_back,
            vec![
                LifecycleEffect::SetClickThrough(true),
                LifecycleEffect::PersistGeometry,
            ]
        );
    }

    #[test]
    fn initial_window_restores_position_before_applying_unlocked_input_state() {
        assert_eq!(
            initial_window_effects(Some(12.0), Some(34.0), false),
            vec![
                LifecycleEffect::RestorePosition(12, 34),
                LifecycleEffect::SetFocusable(true),
                LifecycleEffect::SetClickThrough(false),
            ]
        );
    }

    #[test]
    fn initial_window_without_geometry_uses_locked_safe_defaults() {
        assert_eq!(
            initial_window_effects(None, None, true),
            vec![
                LifecycleEffect::SetFocusable(false),
                LifecycleEffect::SetClickThrough(true),
            ]
        );
    }
}
