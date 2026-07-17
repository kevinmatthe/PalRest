#[cfg(any(feature = "native", test))]
#[derive(Clone, Copy)]
struct MenuItemSpec {
    id: &'static str,
    label: &'static str,
    enabled: bool,
}

#[cfg(any(feature = "native", test))]
const MENU_ITEMS: [MenuItemSpec; 6] = [
    MenuItemSpec {
        id: "status",
        label: "Status: monitoring",
        enabled: false,
    },
    MenuItemSpec {
        id: "adjust",
        label: "Adjust position",
        enabled: true,
    },
    MenuItemSpec {
        id: "lock",
        label: "Lock overlay",
        enabled: true,
    },
    MenuItemSpec {
        id: "settings",
        label: "Settings",
        enabled: true,
    },
    MenuItemSpec {
        id: "reselect",
        label: "Reselect player",
        enabled: true,
    },
    MenuItemSpec {
        id: "quit",
        label: "Quit",
        enabled: true,
    },
];

#[cfg(any(feature = "native", test))]
pub(crate) fn should_show_settings_on_launch(has_valid_config: bool) -> bool {
    !has_valid_config
}

#[cfg(any(feature = "native", test))]
pub(crate) fn tray_icon_is_template(platform: &str) -> bool {
    platform == "macos"
}

#[cfg(feature = "native")]
mod native {
    use super::MENU_ITEMS;
    use crate::lifecycle::{LifecycleController, LifecycleEvent};
    use tauri::{
        App, Emitter, Manager,
        menu::{Menu, MenuItem},
        tray::TrayIconBuilder,
    };

    pub fn setup(app: &mut App, show_settings_on_launch: bool) -> tauri::Result<()> {
        let [
            status_spec,
            adjust_spec,
            lock_spec,
            settings_spec,
            reselect_spec,
            quit_spec,
        ] = MENU_ITEMS;
        let status = menu_item(app, status_spec)?;
        let adjust = menu_item(app, adjust_spec)?;
        let lock = menu_item(app, lock_spec)?;
        let settings = menu_item(app, settings_spec)?;
        let reselect = menu_item(app, reselect_spec)?;
        let quit = menu_item(app, quit_spec)?;
        let menu = Menu::with_items(app, &[&status, &adjust, &lock, &settings, &reselect, &quit])?;

        TrayIconBuilder::new()
            .icon(
                app.default_window_icon()
                    .ok_or_else(|| tauri::Error::AssetNotFound("default window icon".into()))?
                    .clone(),
            )
            .icon_as_template(super::tray_icon_is_template(std::env::consts::OS))
            .tooltip("PalREST Game Overlay")
            .menu(&menu)
            .show_menu_on_left_click(true)
            .on_menu_event(|app, event| match event.id().as_ref() {
                "adjust" => {
                    let _ = app
                        .state::<LifecycleController>()
                        .transition(app, LifecycleEvent::EnterAdjustment);
                }
                "lock" => {
                    let _ = app
                        .state::<LifecycleController>()
                        .transition(app, LifecycleEvent::Lock);
                }
                "settings" => show_settings(app),
                "reselect" => {
                    show_settings(app);
                    let _ = app.emit("reselect-player", ());
                }
                "quit" => {
                    let _ = app
                        .state::<LifecycleController>()
                        .transition(app, LifecycleEvent::Quit);
                    app.exit(0);
                }
                _ => {}
            })
            .build(app)?;
        if show_settings_on_launch {
            show_settings(app.handle());
        }
        Ok(())
    }

    fn menu_item(app: &App, spec: super::MenuItemSpec) -> tauri::Result<MenuItem<tauri::Wry>> {
        MenuItem::with_id(app, spec.id, spec.label, spec.enabled, None::<&str>)
    }

    fn show_settings(app: &tauri::AppHandle) {
        if let Some(settings) = app.get_webview_window("settings") {
            let _ = settings.show();
            let _ = settings.set_focus();
        }
    }
}

#[cfg(feature = "native")]
pub use native::setup;

#[cfg(test)]
mod tests {
    use super::{MENU_ITEMS, should_show_settings_on_launch, tray_icon_is_template};

    #[test]
    fn tray_contract_contains_only_the_six_requested_items() {
        let ids = MENU_ITEMS.map(|item| item.id);
        assert_eq!(
            ids,
            ["status", "adjust", "lock", "settings", "reselect", "quit"]
        );
        assert!(!MENU_ITEMS[0].enabled);
        assert_eq!(MENU_ITEMS[0].label, "Status: monitoring");
        assert!(
            MENU_ITEMS
                .iter()
                .all(|item| !matches!(item.id, "autostart" | "hotkey"))
        );
    }

    #[test]
    fn first_launch_opens_settings_but_configured_launch_stays_in_background() {
        assert!(should_show_settings_on_launch(false));
        assert!(!should_show_settings_on_launch(true));
    }

    #[test]
    fn macos_uses_a_template_menu_bar_icon() {
        assert!(tray_icon_is_template("macos"));
        assert!(!tray_icon_is_template("windows"));
    }
}
