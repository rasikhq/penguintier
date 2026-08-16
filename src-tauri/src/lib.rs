// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
#[tauri::command]
fn greet(name: &str) -> String {
    format!("Hello, {}! You've been greeted from Rust!", name)
}

use std::process::Command;

#[tauri::command]
fn read_root_token() -> Result<String, String> {
    let output = Command::new("pkexec")
        .arg("cat")
        .arg("/var/lib/zerotier-one/authtoken.secret")
        .output()
        .map_err(|e| e.to_string())?;

    if output.status.success() {
        let token = String::from_utf8_lossy(&output.stdout).to_string();
        Ok(token.trim().to_string())
    } else {
        let error = String::from_utf8_lossy(&output.stderr).to_string();
        Err(format!("Authorization failed: {}", error))
    }
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![greet])
        .invoke_handler(tauri::generate_handler![read_root_token])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
