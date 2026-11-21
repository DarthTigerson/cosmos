const { contextBridge, ipcRenderer, webFrame } = require('electron')

// The window learns where the core is and the token to reach it, and can
// scale itself for the text size chosen in the footer.
contextBridge.exposeInMainWorld('cosmos', {
  conn: () => ipcRenderer.invoke('cosmos:conn'),
  setZoom: (factor) => webFrame.setZoomFactor(factor),
  onTextSize: (handler) => {
    const listener = (_e, dir) => handler(dir)
    ipcRenderer.on('cosmos:text-size', listener)
    return () => ipcRenderer.removeListener('cosmos:text-size', listener)
  },
})
