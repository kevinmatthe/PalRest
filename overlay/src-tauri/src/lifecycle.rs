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

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct MonitorBounds {
    x: i32,
    y: i32,
    width: u32,
    height: u32,
}

impl MonitorBounds {
    #[must_use]
    pub const fn new(x: i32, y: i32, width: u32, height: u32) -> Self {
        Self {
            x,
            y,
            width,
            height,
        }
    }

    fn contains(self, x: i32, y: i32) -> bool {
        let x = i64::from(x);
        let y = i64::from(y);
        let left = i64::from(self.x);
        let top = i64::from(self.y);
        x >= left
            && x < left + i64::from(self.width)
            && y >= top
            && y < top + i64::from(self.height)
    }
}

#[must_use]
pub fn position_is_available(x: i32, y: i32, monitors: &[MonitorBounds]) -> bool {
    monitors.iter().any(|monitor| monitor.contains(x, y))
}

#[must_use]
pub fn initial_window_effects(
    x: Option<f64>,
    y: Option<f64>,
    locked: bool,
    overlay_visible: bool,
    monitors: &[MonitorBounds],
) -> Vec<LifecycleEffect> {
    let mut effects = Vec::with_capacity(4);
    if let (Some(x), Some(y)) = (x, y) {
        let x = x.round() as i32;
        let y = y.round() as i32;
        if position_is_available(x, y, monitors) {
            effects.push(LifecycleEffect::RestorePosition(x, y));
        }
    }
    if overlay_visible {
        effects.push(LifecycleEffect::Show);
    }
    effects.push(LifecycleEffect::SetFocusable(!locked));
    effects.push(LifecycleEffect::SetClickThrough(locked));
    effects
}

#[must_use]
pub fn initial_lifecycle_state(
    locked: bool,
    has_valid_config: bool,
    platform: &str,
) -> LifecycleState {
    let state = LifecycleState {
        adjustment: !locked,
        click_through: locked,
        ..LifecycleState::default()
    };
    configured_visibility_event(platform, has_valid_config)
        .map_or(state, |event| reduce(state, event))
}

#[must_use]
pub fn configured_visibility_event(
    platform: &str,
    has_valid_config: bool,
) -> Option<LifecycleEvent> {
    if platform == "macos" {
        has_valid_config.then_some(LifecycleEvent::GameDetected(true))
    } else {
        None
    }
}

#[must_use]
pub fn monitor_visibility_event(platform: &str, detected: bool) -> Option<LifecycleEvent> {
    (platform != "macos").then_some(LifecycleEvent::GameDetected(detected))
}

#[must_use]
pub fn focus_rollback_effects(platform: &str) -> Vec<LifecycleEffect> {
    if needs_reliable_defocus(platform) {
        vec![
            LifecycleEffect::Hide,
            LifecycleEffect::SetFocusable(false),
            LifecycleEffect::Show,
        ]
    } else {
        vec![LifecycleEffect::SetFocusable(false)]
    }
}

fn needs_reliable_defocus(platform: &str) -> bool {
    matches!(platform, "windows" | "macos")
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
            if needs_reliable_defocus(platform) && next.overlay_visible {
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
        LifecycleEffect, LifecycleEffectExecutor, LifecycleEvent, LifecycleState, MonitorBounds,
        SCAN_INTERVAL, execute_effects, focus_rollback_effects, initial_lifecycle_state,
        initial_window_effects, monitor_visibility_event, plan_transition,
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

        fn set_initial_state(&self, state: LifecycleState) -> Result<(), String> {
            *self
                .state
                .lock()
                .map_err(|_| "lifecycle state is unavailable".to_string())? = state;
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
                    for rollback in focus_rollback_effects(current_platform()) {
                        match rollback {
                            LifecycleEffect::Hide => {
                                let _ = self.overlay.hide();
                            }
                            LifecycleEffect::Show => {
                                let _ = self.overlay.show();
                            }
                            LifecycleEffect::SetFocusable(value) => {
                                let _ = self.overlay.set_focusable(value);
                            }
                            _ => {}
                        }
                    }
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
        let initial_state = initial_lifecycle_state(locked, saved.is_some(), current_platform());
        let monitors = overlay
            .available_monitors()
            .unwrap_or_default()
            .into_iter()
            .map(|monitor| {
                let position = monitor.position();
                let size = monitor.size();
                MonitorBounds::new(position.x, position.y, size.width, size.height)
            })
            .collect::<Vec<_>>();
        execute_effects(
            &mut NativeExecutor {
                app,
                overlay,
                geometry_backup: None,
            },
            &initial_window_effects(x, y, locked, initial_state.overlay_visible, &monitors),
        )?;
        app.state::<LifecycleController>()
            .set_initial_state(initial_state)
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
                if platform != "macos" {
                    let detected = processes.palworld_is_running(platform);
                    if let Some(event) = monitor_visibility_event(platform, detected) {
                        let _ = lifecycle.transition(&app, event);
                    }
                }
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
        MonitorBounds, SCAN_INTERVAL, close_action, configured_visibility_event, execute_effects,
        focus_rollback_effects, initial_lifecycle_state, initial_window_effects,
        monitor_visibility_event, plan_transition, reduce,
    };
    use std::time::Duration;

    #[test]
    fn restores_only_positions_inside_a_current_available_monitor() {
        let monitors = [
            MonitorBounds::new(-1920, 0, 1920, 1080),
            MonitorBounds::new(0, 0, 2560, 1440),
        ];

        assert!(super::position_is_available(-100, 100, &monitors));
        assert!(super::position_is_available(2000, 1000, &monitors));
        assert!(!super::position_is_available(3000, 100, &monitors));
        assert!(!super::position_is_available(0, 1440, &monitors));
        assert!(!super::position_is_available(0, 0, &[]));
    }

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
        let monitors = [MonitorBounds::new(0, 0, 1920, 1080)];
        assert_eq!(
            initial_window_effects(Some(12.0), Some(34.0), false, false, &monitors),
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
            initial_window_effects(None, None, true, false, &[]),
            vec![
                LifecycleEffect::SetFocusable(false),
                LifecycleEffect::SetClickThrough(true),
            ]
        );
    }

    #[test]
    fn configured_macos_preloads_visible_lifecycle_state() {
        let locked = initial_lifecycle_state(true, true, "macos");
        assert!(locked.game_running);
        assert!(locked.overlay_visible);
        assert!(!locked.adjustment);
        assert!(locked.click_through);

        let unlocked = initial_lifecycle_state(false, true, "macos");
        assert!(unlocked.game_running);
        assert!(unlocked.overlay_visible);
        assert!(unlocked.adjustment);
        assert!(!unlocked.click_through);
    }

    #[test]
    fn windows_and_unconfigured_macos_start_hidden_until_their_visibility_signal() {
        for state in [
            initial_lifecycle_state(true, true, "windows"),
            initial_lifecycle_state(true, false, "macos"),
        ] {
            assert!(!state.game_running);
            assert!(!state.overlay_visible);
            assert!(state.click_through);
        }
    }

    #[test]
    fn configured_macos_initial_effects_show_without_focus_and_preserve_locking() {
        let effects = initial_window_effects(None, None, true, true, &[]);
        assert_eq!(
            effects,
            vec![
                LifecycleEffect::Show,
                LifecycleEffect::SetFocusable(false),
                LifecycleEffect::SetClickThrough(true),
            ]
        );
        assert!(!effects.contains(&LifecycleEffect::Focus));
    }

    #[test]
    fn unlocked_config_preloads_full_adjustment_state_without_focus() {
        let state = initial_lifecycle_state(false, true, "windows");
        assert!(state.adjustment);
        assert!(!state.click_through);
        assert!(!state.overlay_visible);
        assert!(
            !initial_window_effects(None, None, false, false, &[])
                .iter()
                .any(|effect| { matches!(effect, LifecycleEffect::Show | LifecycleEffect::Focus) })
        );
        assert!(reduce(state, LifecycleEvent::GameDetected(false)).overlay_visible);
    }

    #[test]
    fn configured_visibility_is_only_a_macos_signal() {
        assert_eq!(
            configured_visibility_event("macos", true),
            Some(LifecycleEvent::GameDetected(true))
        );
        assert_eq!(configured_visibility_event("macos", false), None);
        assert_eq!(configured_visibility_event("windows", true), None);
    }

    #[test]
    fn background_monitor_never_drives_macos_visibility() {
        assert_eq!(monitor_visibility_event("macos", false), None);
        assert_eq!(monitor_visibility_event("macos", true), None);
    }

    #[test]
    fn background_monitor_forwards_windows_process_detection() {
        assert_eq!(
            monitor_visibility_event("windows", false),
            Some(LifecycleEvent::GameDetected(false))
        );
        assert_eq!(
            monitor_visibility_event("windows", true),
            Some(LifecycleEvent::GameDetected(true))
        );
    }

    #[test]
    fn focus_rollback_is_reliable_on_macos_and_non_focusing_elsewhere() {
        assert_eq!(
            focus_rollback_effects("macos"),
            vec![
                LifecycleEffect::Hide,
                LifecycleEffect::SetFocusable(false),
                LifecycleEffect::Show,
            ]
        );
        assert_eq!(
            focus_rollback_effects("windows"),
            vec![
                LifecycleEffect::Hide,
                LifecycleEffect::SetFocusable(false),
                LifecycleEffect::Show,
            ]
        );
        assert_eq!(
            focus_rollback_effects("linux"),
            vec![LifecycleEffect::SetFocusable(false)]
        );
    }

    #[test]
    fn windows_lock_uses_the_same_reliable_defocus_sequence_as_macos() {
        let adjusted = LifecycleState {
            game_running: true,
            adjustment: true,
            overlay_visible: true,
            click_through: false,
            quitting: false,
        };
        let effects = plan_transition(adjusted, LifecycleEvent::Lock, "windows").effects;
        assert_eq!(
            &effects[2..5],
            &[
                LifecycleEffect::Hide,
                LifecycleEffect::SetFocusable(false),
                LifecycleEffect::Show,
            ]
        );
    }
}
