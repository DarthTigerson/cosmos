const { contextBridge, ipcRenderer } = require('electron')

// The window only learns where the core is and the token to reach it.
contextBridge.exposeInMainWorld('cosmos', {
  conn: () => ipcRenderer.invoke('cosmos:conn'),
})
