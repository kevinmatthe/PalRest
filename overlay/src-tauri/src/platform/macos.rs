/// macOS has no low-cost, reliable mapping from an OS account to a Palworld UID.
/// Identity therefore always comes from the player's explicit saved selection.
pub(super) fn detected_palworld_user_id() -> Option<String> {
    None
}

#[cfg(test)]
mod tests {
    use super::detected_palworld_user_id;
    use crate::{
        config::{OverlayConfig, load_from_path, save_to_path},
        platform::matches_palworld_process,
    };
    use std::fs;

    #[test]
    fn automatic_identity_discovery_is_never_attempted() {
        assert_eq!(detected_palworld_user_id(), None);
    }

    #[test]
    fn process_detection_requires_an_exact_app_bundle_component() {
        assert!(matches_palworld_process(
            "macos",
            "Palworld",
            Some("/Applications/Palworld.app/Contents/MacOS/Palworld")
        ));
        assert!(!matches_palworld_process(
            "macos",
            "Palworld",
            Some("/Applications/NotPalworld.app/Contents/MacOS/Palworld")
        ));
    }

    #[test]
    fn manually_selected_uid_survives_config_reload() {
        let dir = std::env::temp_dir().join(format!(
            "palrest-overlay-macos-manual-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let config = OverlayConfig {
            schema: 1,
            base_url: "https://palbox.test".into(),
            game_id: "palworld".into(),
            user_id: "steam_manual_42".into(),
            scale: 1.0,
            display_id: Some("display-hint".into()),
            x: Some(10.0),
            y: Some(20.0),
            locked: true,
        };

        save_to_path(&dir, &config).unwrap();
        assert_eq!(load_from_path(&dir).unwrap(), Some(config));
        fs::remove_dir_all(dir).unwrap();
    }
}
