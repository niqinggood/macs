(function () {
    'use strict';
    var rendererPromise, exportStylePromise, busy = false;
    var styleURL = new URL('/web/help_export.css', location.href).href;
    function t(key, fallback) { return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback; }
    function setBusy(value) {
        busy = value;
        document.querySelectorAll('.help-export-actions button').forEach(function (button) { button.disabled = value; });
    }
    function loadRenderer() {
        if (typeof window.html2canvas === 'function') return Promise.resolve(window.html2canvas);
        if (!rendererPromise) rendererPromise = new Promise(function (resolve, reject) {
            var script = document.createElement('script');
            var timer = setTimeout(function () { fail(); }, 20000);
            function fail() { clearTimeout(timer); script.remove(); rendererPromise = null; reject(new Error(t('helpExportFailed', '导出失败，请重试。'))); }
            script.src = '/web/html2canvas.min.js';
            script.onload = function () { clearTimeout(timer); typeof window.html2canvas === 'function' ? resolve(window.html2canvas) : fail(); };
            script.onerror = fail;
            document.head.appendChild(script);
        });
        return rendererPromise;
    }
    function loadStyles() {
        if (!exportStylePromise) exportStylePromise = (async function () {
            var controller = new AbortController(), timer = setTimeout(function () { controller.abort(); }, 10000);
            try {
                var response = await fetch(styleURL, { credentials: 'same-origin', signal: controller.signal });
                if (!response.ok) throw new Error(t('helpExportFailed', '导出失败，请重试。'));
                return await response.text();
            } catch (error) { exportStylePromise = null; throw error; }
            finally { clearTimeout(timer); }
        }());
        return exportStylePromise;
    }
    async function prepare(target) {
        var source = document.querySelector('#helpModal .help-modal-body');
        if (!source) throw new Error(t('helpExportFailed', '导出失败，请重试。'));
        var css = await loadStyles(), doc = target.document;
        doc.open();
        doc.write('<!doctype html><html><head><meta charset="utf-8"></head><body></body></html>');
        doc.close();
        doc.documentElement.lang = document.documentElement.lang || 'zh';
        doc.title = t('helpExportDocumentTitle', 'macs 使用说明');
        doc.body.replaceChildren();
        doc.body.style.margin = '0';
        doc.body.style.background = '#fff';
        var base = doc.createElement('base'); base.href = location.href; doc.head.appendChild(base);
        var stylesheet = doc.createElement('style'); stylesheet.textContent = css;
        doc.head.appendChild(stylesheet);
        var content = source.cloneNode(true);
        content.querySelectorAll('script,style,button,input,textarea,select,nav,[data-help-export-exclude],[hidden],[role="status"]').forEach(function (node) { node.remove(); });
        [content].concat(Array.from(content.querySelectorAll('*'))).forEach(function (node) {
            node.removeAttribute('style');
            Array.from(node.attributes).forEach(function (attribute) { if (/^on/i.test(attribute.name)) node.removeAttribute(attribute.name); });
        });
        content.className = 'macs-help-export';
        // The whole guide is exported; code remains readable without the screen's dark theme.
        content.querySelectorAll('details').forEach(function (details) { details.open = true; });
        var heading = doc.createElement('h1'); heading.textContent = doc.title; content.prepend(heading);
        var footer = doc.createElement('p'); footer.className = 'help-export-footer'; footer.textContent = t('helpExportFooter', '由 macs 导出'); content.appendChild(footer);
        doc.body.appendChild(content);
        if (doc.fonts) await doc.fonts.ready;
        return content;
    }
    function fail(error) { window.alert(error.message || t('helpExportFailed', '导出失败，请重试。')); }
    window.exportHelpAsImage = async function () {
        if (busy) return;
        setBusy(true);
        var frame = document.createElement('iframe');
        frame.title = t('helpExportDocumentTitle', 'macs 使用说明');
        frame.setAttribute('aria-hidden', 'true');
        frame.style.cssText = 'position:fixed;left:-10000px;top:0;width:860px;height:1000px;border:0;pointer-events:none';
        document.body.appendChild(frame);
        try {
            var render = await loadRenderer();
            var content = await prepare(frame.contentWindow);
            var width = Math.ceil(content.getBoundingClientRect().width);
            var height = Math.ceil(content.scrollHeight);
            // Bound each canvas to avoid empty or black output when a guide becomes very long.
            var chunk = 7200, pages = Math.ceil(height / chunk);
            for (var page = 0; page < pages; page++) {
                var canvas = await render(content, { scale: 1.5, width: width, height: Math.min(chunk, height - page * chunk), x: 0, y: page * chunk, scrollX: 0, scrollY: 0, windowWidth: width, windowHeight: 1000, backgroundColor: '#fff', logging: false });
                var blob = await new Promise(function (resolve, reject) { canvas.toBlob(function (value) { value ? resolve(value) : reject(new Error(t('helpExportFailed', '导出失败，请重试。'))); }, 'image/png'); });
                var url = URL.createObjectURL(blob), link = document.createElement('a');
                link.download = 'macs-help' + (pages > 1 ? '-' + (page + 1) : '') + '.png'; link.href = url; document.body.appendChild(link); link.click(); link.remove();
                setTimeout(function (value) { URL.revokeObjectURL(value); }, 60000, url);
                canvas.width = 0; canvas.height = 0;
            }
        } catch (error) { fail(error); }
        finally { frame.remove(); setBusy(false); }
    };
    window.exportHelpAsPDF = async function () {
        if (busy) return;
        var target = window.open('', '_blank', 'width=860,height=760');
        if (!target) { fail(new Error(t('helpExportPopupBlocked', '请允许打开导出窗口后重试。'))); return; }
        setBusy(true);
        try {
            await prepare(target);
            if (!target.closed) { target.focus(); target.print(); }
        } catch (error) { if (!target.closed) target.close(); fail(error); }
        finally { setBusy(false); }
    };
}());
