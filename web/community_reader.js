(function () {
    'use strict';
    var requestNumber = 0;
    var pendingController;
    var headingNodes = [];
    var progressPending = false;
    var embedded = window.parent !== window && new URLSearchParams(location.search).get('embedded') === '1';
    document.documentElement.classList.toggle('reader-embedded', embedded);
    function el(id) { return document.getElementById(id); }
    function safeURL(value) {
        if (!value || !String(value).trim()) return '';
        try {
            var target = new URL(value, location.href);
            return ['http:', 'https:'].includes(target.protocol) && !target.username && !target.password ? target.href : '';
        } catch (error) { return ''; }
    }
    function escapeHTML(value) {
        return String(value).replace(/[&<>"']/g, function (char) { return {'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;'}[char]; });
    }
    function renderMarkdown(content) {
        var template = document.createElement('template');
        var escaped = escapeHTML(content);
        template.innerHTML = window.marked ? window.marked.parse(content, {gfm: true, breaks: false}) : '<p>' + escaped.replace(/\n/g, '<br>') + '</p>';
        var allowed = new Set(['P','BR','H1','H2','H3','H4','H5','H6','STRONG','EM','DEL','BLOCKQUOTE','UL','OL','LI','PRE','CODE','HR','A','IMG','TABLE','THEAD','TBODY','TR','TH','TD']);
        var blocked = new Set(['SCRIPT','STYLE','IFRAME','OBJECT','EMBED','SVG','MATH','LINK','META','BASE','FORM','INPUT','BUTTON','TEXTAREA','SELECT']);
        Array.from(template.content.querySelectorAll('*')).forEach(function (node) {
            if (blocked.has(node.tagName.toUpperCase())) { node.remove(); return; }
            if (!allowed.has(node.tagName)) { node.replaceWith(document.createTextNode(node.textContent)); return; }
            var href = node.tagName === 'A' ? safeURL(node.getAttribute('href')) : '';
            var src = node.tagName === 'IMG' ? safeURL(node.getAttribute('src')) : '';
            var alt = node.tagName === 'IMG' ? node.getAttribute('alt') || '' : '';
            Array.from(node.attributes).forEach(function (attr) { node.removeAttribute(attr.name); });
            if (href) { node.href = href; node.target = '_blank'; node.rel = 'noopener noreferrer'; }
            if (node.tagName === 'IMG') {
                if (!src) { node.remove(); return; }
                node.src = src; node.alt = alt; node.loading = 'lazy'; node.referrerPolicy = 'no-referrer';
                node.addEventListener('error', function () { node.replaceWith(document.createTextNode(alt ? '图片暂不可用：' + alt : '图片暂不可用')); });
            }
        });
        template.content.querySelectorAll('table').forEach(function (table) {
            var wrap = document.createElement('div'); wrap.className = 'reader-table-wrap';
            table.replaceWith(wrap); wrap.appendChild(table);
        });
        el('readerBody').replaceChildren(template.content);
    }
    function renderContents() {
        var nav = el('readerContents'); var mobile = el('readerMobileNav');
        nav.replaceChildren(); mobile.replaceChildren();
        headingNodes = Array.from(el('readerBody').querySelectorAll('h1, h2, h3'));
        document.querySelector('.reader-layout').classList.toggle('no-contents', headingNodes.length === 0);
        headingNodes.forEach(function (heading, index) {
            heading.id = 'section-' + index;
            var link = document.createElement('a'); link.href = '#' + heading.id; link.textContent = heading.textContent;
            if (heading.tagName === 'H3') link.className = 'nested';
            nav.appendChild(link); mobile.appendChild(link.cloneNode(true));
        });
        el('readerSidebar').hidden = headingNodes.length === 0;
        el('readerMobileContents').hidden = headingNodes.length === 0;
    }
    function updateProgress() {
        progressPending = false;
        var body = el('readerBody').getBoundingClientRect();
        var distance = Math.max(1, body.height - window.innerHeight * .65);
        var fraction = Math.max(0, Math.min(1, (window.innerHeight * .2 - body.top) / distance));
        el('readingProgress').style.width = (fraction * 100) + '%';
        var selected = 0;
        headingNodes.forEach(function (heading, index) { if (heading.getBoundingClientRect().top < 180) selected = index; });
        el('readerContents').querySelectorAll('a').forEach(function (link, index) {
            link.classList.toggle('active', selected === index);
            if (selected === index) link.setAttribute('aria-current', 'location'); else link.removeAttribute('aria-current');
        });
    }
    function renderArticle(post) {
        var content = String(post.content || ''); var lines = content.split(/\r?\n/);
        var first = (lines[0] || '').trim();
        var title = first.replace(/^#{1,6}\s+/, '').replace(/[*_`]/g, '').trim();
        if (!title || title.length > 240) title = post.title || '科技前沿';
        if (/^#{1,6}\s+/.test(first) || first === title) content = lines.slice(1).join('\n').trim();
        el('readerTitle').textContent = title; document.title = title + ' · 科技前沿 · macs';
        var author = post.author || '科技社区'; el('readerAuthor').textContent = author;
        el('readerAvatar').textContent = Array.from(author.replace(/^🤖\s*/, ''))[0] || '科';
        var date = new Date(post.created_at); el('readerDate').textContent = Number.isNaN(date.getTime()) ? '' : date.toLocaleString('zh-CN', {year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit'});
        if (!Number.isNaN(date.getTime())) el('readerDate').dateTime = date.toISOString();
        el('readerDuration').textContent = '约 ' + Math.max(1, Math.ceil(content.length / 500)) + ' 分钟阅读';
        renderMarkdown(content); renderContents();
        var source = safeURL(post.community_url); var sourceLink = el('readerCommunityLink');
        sourceLink.hidden = !source; if (source) sourceLink.href = source;
        el('readerState').hidden = true; el('readerArticle').hidden = false; updateProgress();
    }
    async function loadArticle() {
        var id = new URLSearchParams(location.search).get('id');
        if (!id || !/^[1-9]\d{0,18}$/.test(id)) {
            el('readerStateTitle').textContent = '这个文章链接不完整'; el('readerStateHint').textContent = '请返回工作台，从“科技前沿”重新打开文章。'; return;
        }
        var sequence = ++requestNumber;
        if (pendingController) pendingController.abort();
        var controller = new AbortController(); pendingController = controller;
        var timeout = setTimeout(function () { controller.abort(); }, 15000);
        el('readerStateTitle').textContent = '正在打开文章'; el('readerStateHint').textContent = '为你整理成更适合阅读的版式…'; el('readerRetry').hidden = true;
        try {
            var response = await fetch('/api/community/post?id=' + encodeURIComponent(id), {signal: controller.signal, cache: 'no-store'});
            var data = await response.json();
            if (!response.ok || !data.ok || !data.post || !data.post.content) throw new Error('article unavailable');
            if (sequence === requestNumber) renderArticle(data.post);
        } catch (error) {
            if (sequence !== requestNumber) return;
            el('readerStateTitle').textContent = '文章暂时打不开'; el('readerStateHint').textContent = '网络连接较慢，或这篇帖子已不可用。可以重新读取，也可以返回工作台选择其他文章。'; el('readerRetry').hidden = false;
        } finally { clearTimeout(timeout); }
    }
    function setTheme(theme) {
        document.documentElement.dataset.readerTheme = theme; el('readerTheme').textContent = theme === 'dark' ? '浅色' : '深色';
        el('readerTheme').setAttribute('aria-label', theme === 'dark' ? '切换浅色主题' : '切换深色主题');
        try { localStorage.setItem('macsCommunityReadingTheme', theme); } catch (error) {}
    }
    var theme = 'light'; try { theme = localStorage.getItem('macsCommunityReadingTheme') || theme; } catch (error) {}
    setTheme(theme);
    el('readerTheme').addEventListener('click', function () { setTheme(document.documentElement.dataset.readerTheme === 'dark' ? 'light' : 'dark'); });
    el('readerTextSize').addEventListener('click', function () {
        var large = document.documentElement.classList.toggle('reader-large-text');
        el('readerTextSize').setAttribute('aria-pressed', String(large));
        el('readerTextSize').textContent = large ? '标准字号' : '大字阅读';
    });
    if (embedded) document.addEventListener('keydown', function (event) {
        if (event.key === 'Escape') { event.preventDefault(); window.parent.postMessage({type: 'macs:reader-close'}, location.origin); }
        if (event.key === 'Tab') {
            var items = Array.from(document.querySelectorAll('button, a[href], summary')).filter(function (item) { return item.getClientRects().length; });
            if (event.shiftKey && event.target === items[0] || !event.shiftKey && event.target === items[items.length - 1]) {
                event.preventDefault(); window.parent.postMessage({type: 'macs:reader-focus', edge: event.shiftKey ? 'last' : 'first'}, location.origin);
            }
        }
    });
    el('readerRetry').addEventListener('click', loadArticle);
    window.addEventListener('scroll', function () { if (!progressPending) { progressPending = true; requestAnimationFrame(updateProgress); } }, {passive: true});
    window.addEventListener('pagehide', function () { if (pendingController) pendingController.abort(); });
    loadArticle();
})();
