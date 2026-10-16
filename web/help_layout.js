(function () {
    'use strict';
    var modal = document.getElementById('helpModal');
    if (!modal) return;

    function t(key, fallback) {
        return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
    }
    function languageFor(pre) {
        var explicit = pre.getAttribute('data-agentos-language');
        if (explicit) return explicit.toUpperCase();
        if (/Config$/.test(pre.id)) return 'YAML';
        if (/Systemd/.test(pre.id)) return 'INI';
        if (/Python/.test(pre.id) || pre.querySelector('#helpAgentOSPythonCode')) return 'PYTHON';
        if (/Request/.test(pre.id)) return 'HTTP / JSON';
        if (/Client|Health|Windows/.test(pre.id)) return 'POWERSHELL';
        var text = pre.textContent.trim();
        if (text.indexOf('python ') === 0) return 'PYTHON';
        if (text.charAt(0) === '{' || text.charAt(0) === '[') return 'JSON';
        return 'SHELL';
    }
    function selectCode(pre) {
        var selection = window.getSelection();
        if (!selection) return;
        var range = document.createRange();
        range.selectNodeContents(pre);
        selection.removeAllRanges();
        selection.addRange(range);
    }
    function refreshCopyButtons() {
        modal.querySelectorAll('.help-code-copy').forEach(function (button) {
            if (!button.disabled) button.querySelector('span').textContent = t('helpCodeCopy', '复制');
        });
    }
    modal.querySelectorAll('.help-agentos pre').forEach(function (pre) {
        if (pre.closest('.help-code-frame')) return;
        var frame = document.createElement('div');
        frame.className = 'help-code-frame';
        var toolbar = document.createElement('div');
        toolbar.className = 'help-code-toolbar';
        var language = document.createElement('span');
        language.textContent = languageFor(pre);
        var button = document.createElement('button');
        button.type = 'button';
        button.className = 'help-code-copy';
        button.setAttribute('data-help-export-exclude', '');
        button.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/></svg><span></span>';
        var status = document.createElement('span');
        status.className = 'visually-hidden';
        status.setAttribute('role', 'status');
        status.setAttribute('aria-live', 'polite');
        status.setAttribute('data-help-export-exclude', '');
        toolbar.append(language, button, status);
        pre.before(frame);
        frame.append(toolbar, pre);
        button.addEventListener('click', async function () {
            button.disabled = true;
            var label;
            try {
                if (!navigator.clipboard || !navigator.clipboard.writeText) throw new Error('Clipboard unavailable');
                await navigator.clipboard.writeText(pre.textContent);
                label = t('helpCodeCopied', '已复制');
            } catch (_) {
                selectCode(pre);
                label = t('helpCodeSelect', '按 Ctrl/Cmd+C 复制');
            }
            button.querySelector('span').textContent = label;
            status.textContent = label;
            setTimeout(function () {
                button.disabled = false;
                button.querySelector('span').textContent = t('helpCodeCopy', '复制');
                status.textContent = '';
            }, 1600);
        });
    });

    var targets = ['helpAgentOSDeploy', 'helpAgentOSStart', 'helpAgentOSDebug', 'helpAgentOSCall'];
    var steps = modal.querySelectorAll('.help-agentos-nav button');
    steps.forEach(function (button, index) {
        button.setAttribute('aria-controls', targets[index]);
        if (index === 0) button.setAttribute('aria-current', 'step');
        button.addEventListener('click', function () {
            steps.forEach(function (step) { step.removeAttribute('aria-current'); });
            button.setAttribute('aria-current', 'step');
        });
    });
    refreshCopyButtons();
    document.addEventListener('openkitty:languagechange', refreshCopyButtons);
}());

