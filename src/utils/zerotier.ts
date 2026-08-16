import { readTextFile } from "@tauri-apps/plugin-fs";
import { ZeroTier } from "../constant";
import { invoke } from "@tauri-apps/api/core";

export const ZeroTierUtils = {
  async getAuthToken(): Promise<string> {
    try {
      const token = await invoke("read_root_token");
      return token as string;
    } catch (error) {
      // This will trigger if the GUI app lacks root permissions
      console.error("Failed to read token:", error);
      return `Failed to read token: ${error}`;
    }
  },

  async getNetworkStatus(ztAuth: string) {
    const result = await fetch("http://localhost:9993/status", {
      headers: {
        "X-ZT1-Auth": ztAuth,
      },
    });

    if (!result.ok) {
      return result.text;
    }

    return await result.json();
  },
};
