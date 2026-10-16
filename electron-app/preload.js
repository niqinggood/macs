// preload.js — secure bridge between Electron and openkitty Web UI
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('openkittyDesktop', {
    // App info
    platform: process.platform,
    isElectron: true,

    // Native dialogs
    showOpenDialog: (options) => ipcRenderer.invoke('dialog:open', options),
    showSaveDialog: (options) => ipcRenderer.invoke('dialog:save', options),

    // Window controls
    minimize: () => ipcRenderer.send('window:minimize'),
    maximize: () => ipcRenderer.send('window:maximize'),
    close: () => ipcRenderer.send('window:close'),

    // App info
    getVersion: () => ipcRenderer.invoke('app:version'),
    getPath: (name) => ipcRenderer.invoke('app:path', name),

    // Notifications
    notify: (title, body) => ipcRenderer.send('notification:show', { title, body }),
});
