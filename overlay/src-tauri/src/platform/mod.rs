use std::path::Path;

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
