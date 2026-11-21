const { contextBridge, ipcRenderer, webFrame } = require('electron')

// The window learns where the core is and the token to reach it, can
// scale itself for the text size chosen in the footer, and can read the
// Mac's memory and battery for the title bar.
contextBridge.exposeInMainWorld('cosmos', {
  conn: () => ipcRenderer.invoke('cosmos:conn'),
  memory: () => ipcRenderer.invoke('cosmos:memory'),
  memoryBreakdown: () => ipcRenderer.invoke('cosmos:memory-breakdown'),
  battery: () => ipcRenderer.invoke('cosmos:battery'),
  setZoom: (factor) => webFrame.setZoomFactor(factor),
  onTextSize: (handler) => {
    const listener = (_e, dir) => handler(dir)
    ipcRenderer.on('cosmos:text-size', listener)
    return () => ipcRenderer.removeListener('cosmos:text-size', listener)
  },
})
