use sysinfo::{ProcessesToUpdate, System};

pub fn is_palworld_process(platform: &str, name: &str, executable_path: Option<&str>) -> bool {
    crate::platform::matches_palworld_process(platform, name, executable_path)
}

pub struct ProcessMonitor {
    system: System,
}

impl Default for ProcessMonitor {
    fn default() -> Self {
        Self {
            system: System::new(),
        }
    }
}

impl ProcessMonitor {
    pub fn palworld_is_running(&mut self, platform: &str) -> bool {
        self.system.refresh_processes(ProcessesToUpdate::All, true);
        self.system.processes().values().any(|process| {
            let name = process.name().to_string_lossy();
            let executable = process.exe().map(|path| path.to_string_lossy());
            is_palworld_process(platform, &name, executable.as_deref())
        })
    }
}

#[cfg(test)]
mod tests {
    use super::is_palworld_process;

    #[test]
    fn windows_matches_only_the_shipping_executable_case_insensitively() {
        assert!(is_palworld_process(
            "windows",
            "PALWORLD-WIN64-SHIPPING.EXE",
            Some(r"C:\\Games\\Palworld-Win64-Shipping.exe")
        ));
        assert!(!is_palworld_process(
            "windows",
            "pal helper.exe",
            Some(r"C:\\Games\\pal helper.exe")
        ));
    }

    #[test]
    fn macos_matches_only_an_exact_palworld_app_path_component() {
        assert!(is_palworld_process(
            "macos",
            "Pal",
            Some("/Applications/Palworld.app/Contents/MacOS/Palworld")
        ));
        assert!(!is_palworld_process(
            "macos",
            "Palworld",
            Some("/Applications/NotPalworld.app/Contents/MacOS/Palworld")
        ));
        assert!(!is_palworld_process("macos", "pal", None));
    }

    #[test]
    fn unsupported_platforms_do_not_guess_from_broad_names() {
        assert!(!is_palworld_process(
            "linux",
            "Palworld-Win64-Shipping.exe",
            None
        ));
    }
}
