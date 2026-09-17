import { contextBridge, ipcRenderer } from "electron";

import type { AppState, DesktopApi, LogService, SettingsPatch, SetupInput } from "@shared/api";

const api: DesktopApi = {
  app: {
    getState: () => ipcRenderer.invoke("app:getState"),
    onState: (cb) => {
      const listener = (_e: Electron.IpcRendererEvent, state: AppState) => cb(state);
      ipcRenderer.on("app:state", listener);
      return () => ipcRenderer.removeListener("app:state", listener);
    },
    getSettings: () => ipcRenderer.invoke("app:getSettings"),
    saveSetup: (input: SetupInput) => ipcRenderer.invoke("app:saveSetup", input),
    updateSettings: (patch: SettingsPatch) => ipcRenderer.invoke("app:updateSettings", patch),
    restart: () => ipcRenderer.invoke("app:restart"),
    revealKek: () => ipcRenderer.invoke("app:revealKek"),
    markKekBackedUp: () => ipcRenderer.invoke("app:markKekBackedUp"),
    getLogs: (service: LogService, lines?: number) => ipcRenderer.invoke("app:getLogs", service, lines),
    openExternal: (url: string) => ipcRenderer.invoke("app:openExternal", url),
    openDataDir: () => ipcRenderer.invoke("app:openDataDir"),
  },
  plaidsync: {
    request: (method, path, body) => ipcRenderer.invoke("plaidsync:request", method, path, body),
  },
  topper: {
    get: (path, search) => ipcRenderer.invoke("topper:get", path, search),
  },
};

contextBridge.exposeInMainWorld("api", api);
