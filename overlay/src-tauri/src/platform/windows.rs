const STEAM_ID64_BASE: u64 = 76_561_197_960_265_728;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum RegistryValueKind {
    Dword,
    Other,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct RegistryValue<'a> {
    kind: RegistryValueKind,
    bytes: &'a [u8],
}

fn steam_user_id_from_base_and_account_id(base: u64, account_id: u32) -> Option<String> {
    if account_id == 0 {
        return None;
    }
    base.checked_add(u64::from(account_id))
        .map(|steam_id| format!("steam_{steam_id}"))
}

fn steam_user_id_from_account_id(account_id: u32) -> Option<String> {
    steam_user_id_from_base_and_account_id(STEAM_ID64_BASE, account_id)
}

fn detected_user_id_from_registry_value(value: Option<RegistryValue<'_>>) -> Option<String> {
    let value = value?;
    if value.kind != RegistryValueKind::Dword {
        return None;
    }
    let account_id = u32::from_le_bytes(value.bytes.try_into().ok()?);
    steam_user_id_from_account_id(account_id)
}

#[cfg(target_os = "windows")]
pub(super) fn detected_palworld_user_id() -> Option<String> {
    use winreg::{
        RegKey,
        enums::{HKEY_CURRENT_USER, REG_DWORD},
    };

    let active_process = RegKey::predef(HKEY_CURRENT_USER)
        .open_subkey(r"Software\Valve\Steam\ActiveProcess")
        .ok()?;
    let active_user = active_process.get_raw_value("ActiveUser").ok()?;
    let kind = if active_user.vtype == REG_DWORD {
        RegistryValueKind::Dword
    } else {
        RegistryValueKind::Other
    };
    detected_user_id_from_registry_value(Some(RegistryValue {
        kind,
        bytes: &active_user.bytes,
    }))
}

#[cfg(test)]
mod tests {
    use super::{
        RegistryValue, RegistryValueKind, STEAM_ID64_BASE, detected_user_id_from_registry_value,
        steam_user_id_from_account_id, steam_user_id_from_base_and_account_id,
    };

    #[test]
    fn converts_a_nonzero_account_id_to_the_exact_steam_user_id() {
        let account_id = 39_734_273;
        let expected = format!("steam_{}", STEAM_ID64_BASE + u64::from(account_id));

        assert_eq!(steam_user_id_from_account_id(account_id), Some(expected));
    }

    #[test]
    fn rejects_zero_and_overflowing_account_ids() {
        assert_eq!(steam_user_id_from_account_id(0), None);
        assert_eq!(steam_user_id_from_base_and_account_id(u64::MAX, 1), None);
    }

    #[test]
    fn accepts_only_an_exact_little_endian_dword_registry_value() {
        let bytes = 39_734_273_u32.to_le_bytes();

        assert_eq!(
            detected_user_id_from_registry_value(Some(RegistryValue {
                kind: RegistryValueKind::Dword,
                bytes: &bytes,
            })),
            steam_user_id_from_account_id(39_734_273)
        );
        assert_eq!(
            detected_user_id_from_registry_value(Some(RegistryValue {
                kind: RegistryValueKind::Other,
                bytes: &bytes,
            })),
            None
        );
        assert_eq!(
            detected_user_id_from_registry_value(Some(RegistryValue {
                kind: RegistryValueKind::Dword,
                bytes: &bytes[..3],
            })),
            None
        );
    }

    #[test]
    fn unavailable_registry_data_never_guesses_an_identity() {
        assert_eq!(detected_user_id_from_registry_value(None), None);
    }
}
