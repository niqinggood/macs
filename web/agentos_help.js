(function () {
    'use strict';
    var modal = document.getElementById('helpModal');
    if (!modal) return;
    modal.querySelectorAll('[data-help-target]').forEach(function (button) {
        button.addEventListener('click', function () {
            var target = document.getElementById(button.dataset.helpTarget);
            if (!target || !modal.contains(target)) return;
            if (target.id === 'helpAgentOS') {
                var advanced = document.getElementById('helpAgentOSAdvanced');
                if (advanced) advanced.open = true;
            }
            modal.querySelectorAll('[data-help-target]').forEach(function (item) {
                if (item === button) item.setAttribute('aria-current', 'location');
                else item.removeAttribute('aria-current');
            });
            if (target.tagName === 'DETAILS') target.open = true;
            target.focus({ preventScroll: true });
            target.scrollIntoView({ block: 'start', behavior: 'instant' });
        });
    });
}());

(function () {
    'use strict';

    var details = document.getElementById('helpAgentOSPythonSource');
    var code = document.getElementById('helpAgentOSPythonCode');
    var download = document.getElementById('helpAgentOSPythonDownload');
    if (!details || !code || !download) return;
    var pending = false;
    var loaded = false;
    details.addEventListener('toggle', async function () {
        if (!details.open || pending || loaded) return;
        pending = true;
        code.textContent = '正在加载 Python 调试脚本…';
        try {
            var response = await fetch(download.href, { cache: 'no-store' });
            if (!response.ok) throw new Error('HTTP ' + response.status);
            var source = await response.text();
            if (source.indexOf('def main():') < 0 || source.indexOf('ArgumentParser') < 0) throw new Error('Invalid script');
            code.textContent = source;
            loaded = true;
        } catch (error) {
            code.textContent = '暂时无法加载代码，请刷新页面重试，或使用上方的下载按钮获取脚本。';
        } finally {
            pending = false;
        }
    });
}());

(function () {
    'use strict';
    var button = document.getElementById('helpAgentOSLocalCheck');
    var result = document.getElementById('helpAgentOSLocalResult');
    var modal = document.getElementById('helpModal');
    if (!button || !result || !modal) return;
    var controller = null;
    var lastResult = null;
    var origin = window.location.origin;
    var isLocal = /^(localhost|127\.0\.0\.1|\[?::1\]?)$/i.test(window.location.hostname);
    var defaults = {
        helpLocalCheck: '检查本机服务', helpLocalChecking: '正在检查…',
        helpLocalSuccess: '服务已启动、已就绪。可调用助手：',
        helpLocalNoRoles: '服务已就绪，但没有可调用助手。请检查助手和岗位配置。',
        helpLocalOnly: '请在运行 macs 的电脑上打开本机网页，再进行这项检查。远程接入请展开下方 AgentOS 指南。',
        helpLocalAuth: '服务要求凭证。请展开下方 AgentOS 指南，使用 Python 脚本填写业务服务凭证。',
        helpLocalSeparate: '当前 Web 端口未提供 AgentOS 接口。可能已配置独立端口，请用 Python 脚本检查实际的 AgentOS 地址。',
        helpLocalNotReady: '服务尚未就绪，请查看运行窗口的错误并稍后重试。',
        helpLocalFailed: '暂时无法完成检查，请确认程序仍在运行并查看日志。',
        helpLocalCancelled: '检查已取消。'
    };
    function t(key) { return typeof window.openKittyT === 'function' ? window.openKittyT(key, defaults[key]) : defaults[key]; }
    function show(key, names) {
        lastResult = { key: key, names: names || [] };
        result.textContent = t(key) + (names || []).join('、');
        result.dataset.state = key === 'helpLocalSuccess' ? 'success' : 'notice';
    }
    document.getElementById('helpAgentOSLocalBase').textContent = isLocal ? origin : 'http://127.0.0.1:8080';
    document.getElementById('helpAgentOSLocalCommand').textContent = 'python agentos_check.py --local --url ' + (isLocal ? origin : 'http://127.0.0.1:8080');

    async function read(path, signal) {
        var response = await fetch(origin + path, { method: 'GET', credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal: signal });
        if (!response.ok) {
            var error = new Error('Service check failed');
            error.status = response.status;
            throw error;
        }
        var data = await response.json();
        if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error('Invalid response');
        return data;
    }
    button.addEventListener('click', async function () {
        if (controller) return;
        if (!isLocal) { show('helpLocalOnly'); return; }
        controller = new AbortController();
        var current = controller;
        var timeout = setTimeout(function () { current.abort(); }, 12000);
        button.disabled = true;
        button.setAttribute('aria-busy', 'true');
        button.textContent = t('helpLocalChecking');
        result.textContent = '';
        try {
            var live = await read('/livez', current.signal);
            if (live.status !== 'alive') { show('helpLocalNotReady'); return; }
            var ready = await read('/readyz', current.signal);
            if (ready.status !== 'ready') { show('helpLocalNotReady'); return; }
            var directory = await read('/api/v1/agent/roles', current.signal);
            if (!Array.isArray(directory.roles) || directory.roles.some(function (role) { return !role || typeof role.role_id !== 'string'; })) throw new Error('Invalid roles');
            var names = directory.roles.map(function (role) { return role.agent_name || role.role_id; });
            show(names.length ? 'helpLocalSuccess' : 'helpLocalNoRoles', names);
        } catch (error) {
            if (error.name === 'AbortError') show(modal.classList.contains('active') ? 'helpLocalFailed' : 'helpLocalCancelled');
            else if (error.status === 401 || error.status === 403) show('helpLocalAuth');
            else if (error.status === 404) show('helpLocalSeparate');
            else if (error.status === 503) show('helpLocalNotReady');
            else show('helpLocalFailed');
        } finally {
            clearTimeout(timeout);
            controller = null;
            button.disabled = false;
            button.removeAttribute('aria-busy');
            button.textContent = t('helpLocalCheck');
        }
    });
    new MutationObserver(function () { if (controller && !modal.classList.contains('active')) controller.abort(); }).observe(modal, { attributes: true, attributeFilter: ['class'] });
    document.addEventListener('openkitty:languagechange', function () {
        button.textContent = t(controller ? 'helpLocalChecking' : 'helpLocalCheck');
        if (lastResult) show(lastResult.key, lastResult.names);
    });
}());
