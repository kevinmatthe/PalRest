use std::path::Path;

#[cfg(any(target_os = "windows", test))]
mod windows;

#[cfg(all(any(feature = "native", test), target_os = "windows"))]
pub(crate) fn detected_palworld_user_id() -> Option<String> {
    windows::detected_palworld_user_id()
}

#[cfg(all(any(feature = "native", test), not(target_os = "windows")))]
pub(crate) fn detected_palworld_user_id() -> Option<String> {
    None
}

pub(crate) fn matches_palworld_process(
    platform: &str,
    name: &str,
    executable_path: Option<&str>,
) -> bool {
    match platform {
        "windows" => name.eq_ignore_ascii_case("Palworld-Win64-Shipping.exe"),
        "macos" => executable_path.is_some_and(|path| {
            Path::new(path)
                .components()
                .any(|component| component.as_os_str() == "Palworld.app")
        }),
        _ => false,
    }
}

#[cfg(all(test, not(target_os = "windows")))]
mod tests {
    #[test]
    fn non_windows_platforms_do_not_offer_a_detected_uid() {
        assert_eq!(super::detected_palworld_user_id(), None);
    }
}
