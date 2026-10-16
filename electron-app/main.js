const { app, BrowserWindow, Tray, Menu, dialog, shell, nativeImage } = require('electron');
const { spawn } = require('child_process');
const path = require('path');
const fs = require('fs');
const http = require('http');

let mainWindow = null;
let tray = null;
let goProcess = null;
const GO_PORT = 8090;
const isDev = !app.isPackaged;

// ── Paths ──────────────────────────────────────────────────────────────────────
function getExeDir() {
    if (isDev) return path.join(__dirname, '..');
    return path.dirname(app.getPath('exe'));
}

function getGoExePath() {
    if (isDev) return path.join(__dirname, '..', 'openkitty.exe');
    return path.join(process.resourcesPath, 'openkitty.exe');
}

// ── Go Backend ────────────────────────────────────────────────────────────────
function startGoBackend() {
    const goPath = getGoExePath();
    if (!fs.existsSync(goPath)) {
        dialog.showErrorBox('MACS 启动失败', `未找到 MACS 后端程序（兼容文件名 openkitty.exe）：\n${goPath}`);
        app.quit();
        return;
    }

    const exeDir = getExeDir();
    console.log(`[MACS] Starting backend: ${goPath}`);
    console.log(`[MACS] Working directory: ${exeDir}`);

    goProcess = spawn(goPath, [], {
        cwd: exeDir,
        stdio: ['ignore', 'pipe', 'pipe'],
        windowsHide: true,
    });

    goProcess.stdout.on('data', (data) => {
        console.log(`[backend] ${data.toString().trim()}`);
    });

    goProcess.stderr.on('data', (data) => {
        console.error(`[backend:err] ${data.toString().trim()}`);
    });

    goProcess.on('error', (err) => {
        console.error(`[backend] Failed to start: ${err.message}`);
    });

    goProcess.on('exit', (code) => {
        console.log(`[backend] Exited with code ${code}`);
        goProcess = null;
    });
}

function stopGoBackend() {
    if (goProcess) {
        console.log('[MACS] Stopping backend...');
        try {
            // Graceful shutdown via health endpoint
            const req = http.get(`http://localhost:${GO_PORT}/api/health`, () => {});
            req.on('error', () => {});
            req.end();
        } catch(e) {}
        setTimeout(() => {
            if (goProcess) {
                goProcess.kill('SIGTERM');
                setTimeout(() => {
                    if (goProcess) goProcess.kill('SIGKILL');
                }, 3000);
            }
        }, 2000);
    }
}

function waitForBackend(retries = 30) {
    return new Promise((resolve) => {
        function check(n) {
            if (n <= 0) { resolve(false); return; }
            const req = http.get(`http://localhost:${GO_PORT}/api/health`, (res) => {
                resolve(true);
            });
            req.on('error', () => setTimeout(() => check(n - 1), 500));
            req.setTimeout(1000, () => { req.destroy(); setTimeout(() => check(n - 1), 500); });
        }
        check(retries);
    });
}

// ── Window ─────────────────────────────────────────────────────────────────────
function createWindow() {
    mainWindow = new BrowserWindow({
        width: 1280,
        height: 820,
        minWidth: 900,
        minHeight: 600,
        title: 'MACS',
        icon: path.join(__dirname, 'assets', 'icon.ico'),
        webPreferences: {
            preload: path.join(__dirname, 'preload.js'),
            nodeIntegration: false,
            contextIsolation: true,
        },
        show: false,
    });

    mainWindow.loadURL(`http://localhost:${GO_PORT}/web`);

    mainWindow.once('ready-to-show', () => {
        mainWindow.show();
    });

    mainWindow.on('close', (e) => {
        // Minimize to tray instead of closing
        if (tray) {
            e.preventDefault();
            mainWindow.hide();
        }
    });

    mainWindow.on('closed', () => {
        mainWindow = null;
    });

    // Open external links in default browser
    mainWindow.webContents.setWindowOpenHandler(({ url }) => {
        shell.openExternal(url);
        return { action: 'deny' };
    });
}

// ── Tray ───────────────────────────────────────────────────────────────────────
function createTray() {
    // Create a simple 16x16 tray icon
    const iconPath = path.join(__dirname, 'assets', 'icon.ico');
    if (!fs.existsSync(iconPath)) {
        // Create a minimal tray icon programmatically
        const icon = nativeImage.createEmpty();
        tray = new Tray(icon);
    } else {
        tray = new Tray(iconPath);
    }

    const contextMenu = Menu.buildFromTemplate([
        { label: '显示 MACS', click: () => { if (mainWindow) mainWindow.show(); } },
        { type: 'separator' },
        { label: '重启服务', click: () => { stopGoBackend(); setTimeout(startGoBackend, 1000); } },
        { type: 'separator' },
        { label: '退出', click: () => { tray = null; stopGoBackend(); app.quit(); } },
    ]);

    tray.setToolTip('MACS 多智能体协作系统');
    tray.setContextMenu(contextMenu);

    tray.on('double-click', () => {
        if (mainWindow) mainWindow.show();
    });
}

// ── App Lifecycle ──────────────────────────────────────────────────────────────
app.whenReady().then(async () => {
    startGoBackend();
    createTray();
    const ready = await waitForBackend();
    createWindow();
    if (!ready) {
        dialog.showErrorBox('MACS 启动超时', 'MACS 后端启动失败，请检查端口 8090 是否被占用。');
    }
});

app.on('window-all-closed', () => {
    // Don't quit on window close — keep running in tray
});

app.on('before-quit', () => {
    tray = null;
    stopGoBackend();
});

app.on('activate', () => {
    if (mainWindow) mainWindow.show();
});
