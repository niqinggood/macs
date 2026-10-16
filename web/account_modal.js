(function () {
    'use strict';
    var modal, form, returnFocus, controller;
    function t(key, fallback) { return window.openKittyT(key, fallback); }
    function close() {
        if (!modal) return;
        modal.style.display = 'none';
        form.reset();
        modal.querySelector('[role="alert"]').textContent = '';
        if (controller) controller.abort();
        if (returnFocus?.isConnected) returnFocus.focus();
    }
    function translate() {
        modal.querySelectorAll('[data-i18n]').forEach(function (node) {
            node.textContent = t(node.dataset.i18n, node.textContent);
        });
        var button = form.querySelector('[type="submit"]');
        button.textContent = t(controller ? 'accountSigningIn' : 'accountLogin', controller ? '登录中…' : '登录');
        var closeButton = modal.querySelector('.close-modal');
        closeButton.title = closeButton.ariaLabel = t('windowClose', '关闭窗口');
    }
    function create() {
        modal = document.createElement('div');
        modal.id = 'accountLoginModal';
        modal.className = 'modal account-login-modal';
        modal.setAttribute('role', 'dialog');
        modal.setAttribute('aria-modal', 'true');
        modal.setAttribute('aria-labelledby', 'accountLoginHeading');
        modal.innerHTML = '<section class="modal-content account-login-surface">' +
            '<header class="modal-header"><h2 id="accountLoginHeading" data-i18n="accountLoginHeading">登录 SICurrent</h2><button type="button" class="close-modal">×</button></header>' +
            '<form class="account-login-form">' +
            '<p class="account-login-intro" data-i18n="accountLoginIntro">登录后使用平台模型，查看积分余额。</p>' +
            '<label for="accountLoginUsername" data-i18n="accountLoginUsername">用户名、邮箱或手机号</label>' +
            '<input id="accountLoginUsername" name="username" type="text" autocomplete="username" required maxlength="128">' +
            '<label for="accountLoginPassword" data-i18n="accountLoginPassword">密码</label>' +
            '<input id="accountLoginPassword" name="password" type="password" autocomplete="current-password" required>' +
            '<p class="account-login-error" role="alert" aria-live="polite"></p>' +
            '<button class="account-login-submit" type="submit" data-i18n="accountLogin">登录</button>' +
            '<a class="account-login-register" target="_blank" rel="noopener noreferrer" data-i18n="accountRegister">注册账号</a></form></section>';
        form = modal.querySelector('form');
        modal.querySelector('.close-modal').addEventListener('click', close);
        modal.addEventListener('click', function (event) { if (event.target === modal) close(); });
        modal.addEventListener('keydown', function (event) {
            if (event.key === 'Escape') { event.preventDefault(); close(); }
            if (event.key !== 'Tab') return;
            var items = Array.from(modal.querySelectorAll('button:not([disabled]),input,a[href]')).filter(function (node) { return node.getClientRects().length; });
            var edge = event.shiftKey ? items[0] : items[items.length - 1];
            if (event.target === edge) { event.preventDefault(); (event.shiftKey ? items[items.length - 1] : items[0]).focus(); }
        });
        form.addEventListener('submit', async function (event) {
            event.preventDefault();
            if (controller) return;
            var username = form.elements.username.value.trim(), password = form.elements.password.value;
            if (!username || !password) return;
            var error = modal.querySelector('[role="alert"]'), button = form.querySelector('[type="submit"]');
            error.textContent = '';
            controller = new AbortController();
            var signal = controller.signal, timeout = setTimeout(function () { controller?.abort(); }, 25000);
            button.disabled = true; translate();
            try {
                var response = await fetch('/api/account/login', {method: 'POST', credentials: 'same-origin', headers: {'Content-Type': 'application/json', 'Accept': 'application/json'},
                    body: JSON.stringify({username: username, password: password, rememberMe: false}), signal: signal});
                var data = await response.json();
                if (!response.ok || !data.ok || !data.user?.username) throw new Error(data.message || t('accountLoginFailed', '登录失败，请检查账号和密码。'));
                controller = null;
                close();
                await window.refreshTopbarAccount();
            } catch (failure) {
                if (modal.style.display !== 'none') error.textContent = failure.name === 'AbortError' ? t('accountLoginTimeout', '登录超时，请稍后重试。') : failure.message || t('accountLoginFailed', '登录失败，请检查账号和密码。');
                if (signal.aborted) window.refreshTopbarAccount();
            } finally {
                clearTimeout(timeout); controller = null; button.disabled = false; translate();
            }
        });
        document.body.appendChild(modal);
        document.addEventListener('keydown', function(event) {
            if(event.key==='Escape' && modal.style.display==='flex' && !modal.classList.contains('mac-window-minimized')) {
                event.preventDefault();event.stopPropagation();close();
            }
        }, true);
        document.addEventListener('openkitty:languagechange', translate);
    }
    window.openAccountLoginModal = async function () {
        if (!modal) create();
        returnFocus = document.activeElement;
        window.closeAccountMenu(false);
        if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(modal);
        translate();
        modal.style.display = 'flex';
        form.elements.username.focus();
        var registration = modal.querySelector('.account-login-register');
        try {
            var response = await fetch('/api/account/config', {headers: {'Accept': 'application/json'}, cache: 'no-store'});
            var data = await response.json(), target = new URL(data.register_url);
            if (target.protocol === 'https:' || target.protocol === 'http:') registration.href = target.href;
        } catch (error) { registration.removeAttribute('href'); }
    };
    window.requirePlatformAccountLogin = async function () {
        if (window.composerLoginCheckPending) return false;
        window.composerLoginCheckPending = true;
        var check = new AbortController(), timeout = setTimeout(function () { check.abort(); }, 8000);
        try {
            var response = await fetch('/api/account/me', {headers: {'Accept': 'application/json'}, credentials: 'same-origin', cache: 'no-store', signal: check.signal});
            var data = await response.json().catch(function () { return {}; });
            if (response.ok && data.authenticated === true && data.user?.username) return true;
            if (response.status === 401 || response.ok && data.authenticated === false) { await window.openAccountLoginModal(); return false; }
            throw new Error(t('accountCheckFailed', '暂时无法确认登录状态，请稍后重试。'));
        } catch (error) {
            if (error.name === 'AbortError') throw new Error(t('accountCheckFailed', '暂时无法确认登录状态，请稍后重试。'));
            throw error;
        } finally { clearTimeout(timeout); window.composerLoginCheckPending = false; }
    };
})();
