(function () {
    'use strict';
    var modal, models = [], kind = 'image', sequence = 0, submitting = false, uncertain = false;
    var pollTimer, readController, returnFocus, activeTask, signedIn = false, uncertainKind = '', checkedUncertain = false, lastUsername = '';
    var catalogStatus = 'loading';
    function t(key, fallback) { return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback; }
    function el(id) { return document.getElementById(id); }
    function node(tag, className, text) {
        var item = document.createElement(tag);
        if (className) item.className = className;
        if (text !== undefined) item.textContent = text;
        return item;
    }
    function icon(name) {
        var paths = {
            image: '<rect x="3" y="3" width="18" height="18" rx="4"/><circle cx="8.5" cy="8.5" r="1.5"/><path d="m4 17 5-5 4 4 3-3 5 5"/>',
            music: '<path d="M9 18V5l12-2v13M9 9l12-2"/><ellipse cx="6" cy="18" rx="3" ry="3"/><ellipse cx="18" cy="16" rx="3" ry="3"/>',
            video: '<rect x="3" y="4" width="18" height="16" rx="3"/><path d="m10 8 6 4-6 4V8Z"/>',
            refresh: '<path d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-1l2 3M4 15l2 3a7 7 0 0 0 12-1"/>',
            spark: '<path d="m12 3 2.3 6.7L21 12l-6.7 2.3L12 21l-2.3-6.7L3 12l6.7-2.3L12 3Z"/>',
            arrow: '<path d="M5 12h14m-5-5 5 5-5 5"/>',
            headphones: '<path d="M4 14v-3a8 8 0 0 1 16 0v3"/><rect x="3" y="12" width="4" height="8" rx="2"/><rect x="17" y="12" width="4" height="8" rx="2"/>',
            expand: '<path d="M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5"/>'
        };
        return '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + paths[name] + '</svg>';
    }
    function safeURL(value) {
        if (!value) return '';
        try {
            var target = new URL(value, location.href);
            return ['http:', 'https:'].includes(target.protocol) && !target.username && !target.password ? target.href : '';
        } catch (error) { return ''; }
    }
    function renderEmpty(text) {
        var result = el('mediaResult'); result.replaceChildren(); result.dataset.status = 'empty';
        var empty = node('div', 'media-result-empty'), art = node('div', 'media-empty-art'); art.setAttribute('aria-hidden', 'true');
        if (kind === 'music') {
            art.innerHTML = '<div class="media-empty-record"><div class="media-empty-record-label">' + icon('music') + '</div></div><div class="media-empty-wave"><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i><i></i></div>';
        } else if (kind === 'image') {
            art.innerHTML = '<svg class="media-empty-landscape" viewBox="0 0 260 180" fill="none"><rect x="27" y="19" width="206" height="142" rx="15" fill="var(--studio-art-sky)" stroke="var(--studio-art-line)"/><circle cx="176" cy="60" r="17" fill="var(--studio-art-sun)"/><path d="m27 129 56-59 52 57 29-32 69 53v-2a15 15 0 0 1-15 15H42a15 15 0 0 1-15-15v-17Z" fill="var(--studio-art-mountain)"/><path d="m27 139 62-34 60 39 42-22 42 25a15 15 0 0 1-15 14H42a15 15 0 0 1-15-15v-7Z" fill="var(--studio-art-foreground)"/><rect x="17" y="34" width="15" height="24" rx="5" fill="var(--studio-art-sun)"/><path d="m222 15 3 8 8 3-8 3-3 8-3-8-8-3 8-3 3-8Z" fill="var(--studio-accent)"/></svg>';
        } else { art.innerHTML = icon('video'); }
        empty.appendChild(art);
        empty.appendChild(node('h4', '', text || (kind === 'image' ? t('mediaImageEmpty', '灵感，从一句描述开始') : kind === 'music' ? t('mediaMusicEmpty', '下一首作品，从这里开始') : t('mediaVideoEmpty', '把动态灵感，留在这里'))));
        empty.appendChild(node('p', '', kind === 'image' ? t('mediaImageEmptyHint', '生成的图片会显示在这块画布上') : kind === 'music' ? t('mediaMusicEmptyHint', '生成后即可试听、查看歌词和下载') : t('mediaVideoEmptyHint', '在对话框生成视频，在这里查看结果')));
        result.appendChild(empty);
    }
    function suggestions() {
        var list = el('mediaPromptSuggestions'); list.replaceChildren();
        var presets = kind === 'music' ? [
            ['mediaPresetLofi', '轻松 Lo-fi', 'mediaDraftLofi', '适合午后阅读的轻松 Lo-fi 配乐，温暖的钢琴、柔和鼓点，无人声。'],
            ['mediaPresetAcoustic', '治愈民谣', 'mediaDraftAcoustic', '一首关于旅途与重逢的治愈民谣，木吉他伴奏，温柔的人声。'],
            ['mediaPresetCinema', '电影配乐', 'mediaDraftCinema', '充满希望的电影配乐，钢琴与弦乐逐渐展开，适合旅行短片。']
        ] : [
            ['mediaPresetProduct', '产品摄影', 'mediaDraftProduct', '白色陶瓷杯的产品摄影，温暖自然光，浅米色背景，细腻质感与干净构图。'],
            ['mediaPresetLandscape', '自然风景', 'mediaDraftLandscape', '清晨薄雾中的山间湖泊，柔和晨光，宁静的绿色山峦，电影感摄影。'],
            ['mediaPresetIllustration', '创意插画', 'mediaDraftIllustration', '一只小猫在月光下的花园里散步，柔和水彩插画，细腻笔触与梦幻色彩。']
        ];
        presets.forEach(function (preset) {
            var button = node('button', '', t(preset[0], preset[1])); button.type = 'button'; button.disabled = submitting;
            button.addEventListener('click', function () { el('mediaPrompt').value = t(preset[2], preset[3]); el('mediaPrompt').focus(); });
            list.appendChild(button);
        });
    }
    function stopReading() {
        clearTimeout(pollTimer);
        if (readController) readController.abort();
        readController = null;
    }
    function close() {
        sequence++; stopReading(); modal.style.display = 'none';
        if (returnFocus && returnFocus.isConnected) returnFocus.focus();
    }
    function price(model) {
        var billing = model && model.billing;
        if (!billing || !billing.unit || billing.currency && billing.currency !== 'POINT') return t('mediaPricePending', '价格待确认');
        if (billing.unit === 'free') return t('mediaFree', '免费');
        if (billing.unit === 'per_success' && typeof billing.price === 'number' && Number.isFinite(billing.price) && billing.price >= 0) return billing.price + ' ' + t('mediaPriceUnit', '积分 / 成功生成');
        return t('mediaPricePending', '价格待确认');
    }
    function updateForm() {
        modal.dataset.mediaKind = kind;
        var selected = models.find(function (item) { return item.id === el('mediaModel').value && item.capability === kind; });
        el('mediaPrice').textContent = catalogStatus === 'loading' ? t('mediaModelsLoading', '正在读取平台模型…') : catalogStatus === 'failed' ? t('mediaModelsFailed', '读取失败，请刷新模型') : selected ? price(selected) : t('mediaModelsEmpty', '暂无可用的生成模型');
        el('mediaModel').disabled = submitting || catalogStatus !== 'ready';
        el('mediaCatalogRefresh').disabled = submitting || catalogStatus === 'loading';
        el('mediaImageOptions').hidden = kind !== 'image';
        el('mediaPromptLabel').hidden = kind === 'video';
        el('mediaTemplates').disabled = submitting;
        el('mediaGenerate').hidden = kind === 'video';
        el('mediaMusicOptions').hidden = kind !== 'music';
        el('mediaLyricsLabel').hidden = kind !== 'music' || el('mediaMusicType').value === 'instrumental';
        el('mediaGenerate').disabled = kind === 'video' || submitting || uncertain || catalogStatus !== 'ready' || !signedIn || !selected || !selected.available || !selected.direct_generation;
        el('mediaGenerate').innerHTML = icon('spark');
        el('mediaGenerate').appendChild(node('span', '', submitting ? t('generationSubmitting', '正在提交…') : uncertain ? t('generationCheckHistory', '核对平台生成记录') : kind === 'image' ? t('mediaGenerateImage', '生成图片') : t('mediaGenerateMusic', '生成音乐')));
        el('mediaGenerate').setAttribute('aria-busy', String(submitting));
        el('mediaLogin').hidden = signedIn;
        el('mediaNewAttempt').hidden = !uncertain || kind !== uncertainKind;
        el('mediaNewAttempt').disabled = !checkedUncertain || submitting || !signedIn;
        el('mediaHeading').textContent = kind === 'image' ? t('mediaImageTitle', '图片生成') : kind === 'video' ? t('mediaVideoTitle', '视频记录') : t('mediaMusicTitle', '音乐生成');
        el('mediaHeadingIcon').innerHTML = icon(kind);
        el('mediaSubtitle').textContent = kind === 'image' ? t('mediaImageSubtitle', '把想象变成画面') : kind === 'music' ? t('mediaMusicSubtitle', '让灵感拥有旋律') : t('mediaVideoSubtitle', '收藏每一个动态瞬间');
        el('mediaOutputTitle').textContent = kind === 'image' ? t('mediaImageCanvas', '预览画布') : kind === 'music' ? t('mediaMusicCanvas', '试听空间') : t('mediaVideoCanvas', '视频预览');
        el('mediaPromptText').textContent = kind === 'image' ? t('mediaImageDescription', '画面描述') : t('mediaMusicDescription', '风格与灵感');
        el('mediaPrompt').placeholder = kind === 'image' ? t('mediaImagePlaceholder', '描述主体、场景和画面风格…') : t('mediaMusicPlaceholder', '描述曲风、情绪、乐器或歌曲主题…');
        el('mediaPromptSuggestions').hidden = kind === 'video'; suggestions();
        modal.querySelectorAll('[data-media-ratio]').forEach(function (button) { button.setAttribute('aria-pressed', String(button.dataset.mediaRatio === el('mediaRatio').value)); button.disabled = submitting; });
        modal.querySelectorAll('[data-media-music-type]').forEach(function (button) { button.setAttribute('aria-pressed', String(button.dataset.mediaMusicType === el('mediaMusicType').value)); button.disabled = submitting; });
        modal.querySelectorAll('[data-media-kind]').forEach(function (button) { button.setAttribute('aria-pressed', String(button.dataset.mediaKind === kind)); });
    }
    function setModels(selected) {
        var select = el('mediaModel'); select.replaceChildren();
        models.filter(function (model) { return model.capability === kind && model.available && model.direct_generation; }).forEach(function (model) {
            var option = node('option', '', model.name || model.model || model.id); option.value = model.id; select.appendChild(option);
        });
        if (selected && Array.from(select.options).some(function (option) { return option.value === selected; })) select.value = selected;
        updateForm();
    }
    function message(text) { el('mediaFeedback').textContent = text || ''; }
    function signedOut() {
        signedIn = false; activeTask = null; stopReading();
        el('mediaPrompt').value = ''; el('mediaLyrics').value = '';
        renderEmpty(t('mediaSignInResults', '登录后查看自己的作品'));
        updateForm();
    }
    function errorText(code) {
        return {
            login_required: '请先登录 SICurrent 账号。',
            platform_not_ready: '平台生成接口尚未上线，请在后端更新后使用。',
            insufficient_funds: '账号余额不足，请充值后再生成。',
            invalid_generation: '模型未接受这些生成参数，请检查描述或歌词。',
            generation_in_progress: '已有音乐正在生成，请从最近生成中查看进度。',
            rate_limited: '请求过于频繁，请稍后再试。',
            submission_unknown: '未能确认提交结果。请先刷新最近生成，避免重复生成和扣费。'
        }[code] || '暂时无法连接平台，请稍后刷新生成记录。';
    }
    async function api(path, options) {
        options = options || {};
        var controller = new AbortController();
        if (!options.method) readController = controller;
        var timeout = setTimeout(function () { controller.abort(); }, 50000);
        try {
            var response = await fetch('/api/platform/media/' + path, Object.assign({cache: 'no-store', credentials: 'same-origin', signal: controller.signal}, options));
            var result = await response.json();
            if (!response.ok || !result.ok) { var error = new Error(errorText(result.code)); error.code = result.code; throw error; }
            return result;
        } finally { clearTimeout(timeout); }
    }
    function renderTask(task) {
        activeTask = task;
        var result = el('mediaResult'); result.replaceChildren();
        result.dataset.status = task.status;
        var labels = {queued: t('generationQueued', '等待生成'), pending: t('generationQueued', '等待生成'), processing: t('generationProcessing', '正在生成'), running: t('generationProcessing', '正在生成'), completed: t('generationCompleted', '生成完成'), failed: t('generationFailed', '生成失败'), stopped: t('generationStopped', '已停止')};
        result.appendChild(node('div', 'media-result-status', labels[task.status] || t('generationChecking', '待核对')));
        if (kind !== 'music' || task.status !== 'completed') result.appendChild(node('p', 'media-result-title', task.title || task.prompt || '#' + task.id));
        if (task.status === 'completed' && task.result_url) {
            var target = safeURL(task.result_url);
            if (target) {
                var preview = node(kind === 'image' ? 'img' : kind === 'video' ? 'video' : 'audio', 'media-result-preview');
                if (kind === 'image') { preview.alt = task.prompt || '生成的图片'; preview.referrerPolicy = 'no-referrer'; }
                else { preview.controls = true; preview.preload = 'none'; }
                preview.src = target;
                if (kind === 'music') {
                    var card = node('div', 'generation-music-card');
                    var cover = node('div', 'generation-music-cover'); cover.setAttribute('aria-hidden', 'true');
                    cover.innerHTML = '<div class="generation-music-disc"><span>♪</span></div>';
                    var body = node('div', 'generation-music-body');
                    body.appendChild(node('span', 'generation-music-caption', t('mediaMusicArtwork', '音乐作品')));
                    body.appendChild(node('strong', 'generation-music-title', task.title || t('generationCompletedMusic', '生成的音乐')));
                    body.appendChild(node('span', 'generation-music-model', 'SICurrent'));
                    preview.className = 'generation-result-player'; preview.setAttribute('aria-label', task.title || t('generationCompletedMusic', '生成的音乐'));
                    body.appendChild(preview); card.appendChild(cover); card.appendChild(body); result.appendChild(card);
                } else { result.appendChild(preview); }
                var download = node('a', 'media-result-download', kind === 'image' ? t('mediaOpenImage', '打开图片') : kind === 'video' ? t('mediaOpenVideo', '打开视频') : t('mediaDownloadMusic', '下载音乐'));
                download.href = target; download.target = '_blank'; download.rel = 'noopener noreferrer';
                if (kind === 'music') download.download = 'macs-music-' + task.id + '.mp3';
                result.appendChild(download);
            }
        } else if (!['completed', 'failed', 'stopped'].includes(task.status)) {
            var working = node('div', 'media-result-working'); working.innerHTML = icon(kind); working.setAttribute('aria-hidden', 'true'); result.appendChild(working);
            var progress = node('progress', 'media-progress'); progress.max = 100; progress.value = Math.max(0, Math.min(100, Number(task.progress) || 0));
            progress.setAttribute('aria-label', t('mediaProgress', '生成进度')); result.appendChild(progress);
            result.appendChild(node('p', 'media-result-hint', t('mediaBackground', '可关闭窗口，稍后在最近生成中查看。')));
        }
        if (task.error_message) result.appendChild(node('p', 'media-result-error', task.error_message));
        if (task.lyrics) {
            var lyrics = node('details', 'media-result-lyrics-panel'); lyrics.appendChild(node('summary', '', t('generationResultLyrics', '歌词'))); lyrics.appendChild(node('pre', 'media-result-lyrics', task.lyrics)); result.appendChild(lyrics);
        }
        var billing = {free: t('mediaFree', '免费'), reserved: t('generationReserved', '费用已预留'), charged: t('mediaSettled', '已结算'), settled: t('mediaSettled', '已结算'), refunded: t('generationRefunded', '已退款'), released: t('generationReleased', '预留已释放'), pending: t('mediaBillingPending', '费用待核对')};
        if (task.payment_status) result.appendChild(node('small', 'media-result-billing', (billing[task.payment_status] || t('mediaBillingPending', '费用待核对')) + (typeof task.cost === 'number' ? ' · ' + task.cost + ' ' + t('generationPoints', '积分') : '')));
        el('mediaHistory').querySelectorAll('[data-media-task]').forEach(function (button) { button.setAttribute('aria-pressed', String(button.dataset.mediaTask === String(task.id))); });
    }
    async function watch(task, token) {
        if (token !== sequence || modal.style.display === 'none') return;
        renderTask(task);
        if (['completed', 'failed', 'stopped'].includes(task.status)) return;
        pollTimer = setTimeout(async function () {
            try {
                var data = await api(kind + '/' + task.id);
                if (token === sequence) watch(data.task, token);
            } catch (error) {
                if (token !== sequence) return;
                message(errorText(error.code));
                if (error.code === 'login_required') signedOut();
            }
        }, 3000);
    }
    async function history(token, resume) {
        var list = el('mediaHistory');
        list.replaceChildren(node('p', 'media-empty', t('mediaHistoryLoading', '正在读取最近生成…')));
        try {
            var data = await api(kind);
            if (token !== sequence) return;
            list.replaceChildren();
            if (!data.tasks.length) list.appendChild(node('p', 'media-empty', t('mediaHistoryEmpty', '还没有作品，试试你的第一个灵感。')));
            data.tasks.slice(0, 10).forEach(function (task) {
                var button = node('button', 'media-history-item'); button.type = 'button'; button.dataset.mediaTask = task.id;
                button.setAttribute('aria-pressed', String(activeTask && String(activeTask.id) === String(task.id)));
                var thumbnail = node('span', 'media-history-thumb'); thumbnail.setAttribute('aria-hidden', 'true');
                var target = kind === 'image' && task.status === 'completed' ? safeURL(task.result_url) : '';
                if (target) { var image = node('img'); image.src = target; image.alt = ''; image.loading = 'lazy'; image.referrerPolicy = 'no-referrer'; thumbnail.appendChild(image); }
                else { thumbnail.innerHTML = icon(kind); }
                var info = node('span', 'media-history-info');
                info.appendChild(node('span', 'media-history-title', task.title || task.prompt || '#' + task.id));
                info.appendChild(node('small', 'media-history-meta', task.status === 'completed' ? t('generationCompleted', '生成完成') : task.status === 'failed' ? t('generationFailed', '生成失败') : task.status === 'stopped' ? t('generationStopped', '已停止') : t('generationProcessing', '正在生成')));
                var arrow = node('span', 'media-history-arrow'); arrow.innerHTML = icon('arrow');
                button.appendChild(thumbnail); button.appendChild(info); button.appendChild(arrow);
                button.addEventListener('click', async function () {
                    stopReading();
                    try { var data = await api(kind + '/' + task.id); if (token === sequence) { message(''); watch(data.task, token); } }
                    catch (error) { if (token === sequence) message(errorText(error.code)); }
                });
                list.appendChild(button);
            });
            if (resume && data.tasks.length) watch(data.tasks[0], token);
            if (uncertain && kind === uncertainKind) { checkedUncertain = true; updateForm(); }
        } catch (error) {
            if (token !== sequence) return;
            list.replaceChildren(node('p', 'media-empty', errorText(error.code)));
            if (error.code === 'login_required') signedOut();
        }
    }
    async function load(selected) {
        var token = ++sequence; stopReading(); signedIn = false; activeTask = null; catalogStatus = 'loading'; updateForm(); message('');
        renderEmpty();
        el('mediaHistory').replaceChildren();
        await Promise.all([
            (async function () {
                try {
                    var result = await read('/api/platform/models');
                    if (!result.response.ok || !result.data.ok || !Array.isArray(result.data.models)) throw new Error('catalog_unavailable');
                    if (token !== sequence) return;
                    models = result.data.models; catalogStatus = 'ready'; setModels(selected);
                } catch (error) {
                    if (token !== sequence) return;
                    models = []; catalogStatus = 'failed'; setModels();
                }
            })(),
            (async function () {
                try {
                    var result = await read('/api/account/me');
                    if (token !== sequence) return;
                    signedIn = result.response.ok && result.data.ok;
                    var username = signedIn && result.data.user ? result.data.user.username : '';
                    if (lastUsername && lastUsername !== username) { el('mediaPrompt').value = ''; el('mediaLyrics').value = ''; uncertain = false; checkedUncertain = false; }
                    lastUsername = username; updateForm();
                    if (signedIn) await history(token, true);
                } catch (error) {
                    if (token === sequence) message('无法确认登录状态，请重新登录；仍可查看平台模型。');
                }
            })()
        ]);
    }
    async function read(path) {
        var controller = new AbortController();
        var timeout = setTimeout(function () { controller.abort(); }, 15000);
        try {
            var response = await fetch(path, {cache: 'no-store', credentials: 'same-origin', signal: controller.signal});
            return {response: response, data: await response.json()};
        } finally { clearTimeout(timeout); }
    }
    async function submit(event) {
        event.preventDefault();
        if (el('mediaGenerate').disabled) return;
        var payload = {model: el('mediaModel').value, prompt: el('mediaPrompt').value.trim()};
        if (!payload.prompt) { el('mediaPrompt').focus(); return; }
        if (kind === 'image') payload.aspect_ratio = el('mediaRatio').value;
        else { payload.instrumental = el('mediaMusicType').value === 'instrumental'; payload.lyrics = payload.instrumental ? '' : el('mediaLyrics').value.trim(); }
        submitting = true; updateForm(); message('正在提交生成请求…'); stopReading();
        var token = sequence, submittedKind = kind;
        try {
            var data = await api(submittedKind, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(payload)});
            if (token === sequence) { message('任务已提交'); watch(data.task, token); }
        } catch (error) {
            if (!error.code || ['submission_unknown', 'invalid_response', 'platform_unavailable'].includes(error.code)) {
                uncertain = true; uncertainKind = submittedKind; checkedUncertain = false;
            }
            if (token === sequence) {
                message(uncertain ? errorText('submission_unknown') : errorText(error.code));
                if (error.code === 'login_required') signedOut();
                if (error.code === 'generation_in_progress') await history(token, true);
            }
        } finally { submitting = false; updateForm(); }
    }
    function create() {
        modal = node('div', 'modal'); modal.id = 'mediaStudioModal'; modal.style.display = 'none';
        modal.setAttribute('role', 'dialog'); modal.setAttribute('aria-modal', 'true'); modal.setAttribute('aria-labelledby', 'mediaHeading');
        modal.innerHTML = '<section class="modal-content media-studio-surface">' +
            '<header class="modal-header media-studio-header"><div class="media-studio-heading"><span id="mediaHeadingIcon" class="media-studio-icon" aria-hidden="true"></span><div><h2 id="mediaHeading">图片生成</h2><p id="mediaSubtitle"></p></div></div><button type="button" id="mediaMaximize" data-window-size-toggle aria-pressed="false">' + icon('expand') + '<span data-window-size-label>最大化窗口</span></button><button type="button" class="close-modal" aria-label="关闭生成窗口">×</button></header>' +
            '<div class="media-studio-tabs"><div class="media-tab-group"><button type="button" data-media-kind="image">' + icon('image') + '<span data-i18n="mediaImageTitle">图片生成</span></button><button type="button" data-media-kind="music">' + icon('music') + '<span data-i18n="mediaMusicTitle">音乐生成</span></button><button type="button" data-media-kind="video">' + icon('video') + '<span data-i18n="mediaVideoTitle">视频记录</span></button></div><span class="media-platform-badge"><i></i>SICurrent <span data-i18n="mediaPlatform">平台</span></span></div>' +
            '<div class="media-studio-layout"><form id="mediaForm" class="media-studio-form"><div class="media-form-block media-model-card"><div class="media-model-heading"><label for="mediaModel" data-i18n="mediaModelLabel">平台模型</label><button id="mediaCatalogRefresh" type="button" title="刷新模型">' + icon('refresh') + '<span data-i18n="mediaRefreshModels">刷新</span></button></div><select id="mediaModel"></select><p id="mediaPrice" class="media-price"></p></div>' +
            '<div class="media-form-block"><div id="mediaPromptLabel"><div class="media-prompt-heading"><label for="mediaPrompt" id="mediaPromptText">画面描述</label><button id="mediaTemplates" type="button" data-i18n="promptTemplatesTitle" aria-haspopup="dialog" aria-controls="promptTemplatesModal">提示词模板</button></div><textarea id="mediaPrompt" maxlength="4000" rows="5" required></textarea></div><div id="mediaPromptSuggestions" class="media-prompt-chips"></div></div>' +
            '<div id="mediaImageOptions" class="media-option-group"><span class="media-field-title" data-i18n="generationRatio">画面比例</span><select id="mediaRatio" hidden aria-hidden="true" tabindex="-1"><option>1:1</option><option>16:9</option><option>9:16</option><option>4:3</option><option>3:4</option></select><div class="media-ratio-options" role="group" aria-label="画面比例">' +
            [['1:1',16,16],['16:9',26,15],['9:16',13,23],['4:3',22,16],['3:4',15,20]].map(function (ratio) { return '<button type="button" data-media-ratio="' + ratio[0] + '"><span class="media-ratio-shape" style="--ratio-width:' + ratio[1] + 'px;--ratio-height:' + ratio[2] + 'px" aria-hidden="true"></span><span>' + ratio[0] + '</span></button>'; }).join('') + '</div></div>' +
            '<div id="mediaMusicOptions" class="media-option-group" hidden><span class="media-field-title" data-i18n="generationMusicType">音乐类型</span><select id="mediaMusicType" hidden aria-hidden="true" tabindex="-1"><option value="song">歌曲</option><option value="instrumental">纯音乐 / 配乐</option></select><div class="media-type-options" role="group" aria-label="音乐类型"><button type="button" data-media-music-type="song">' + icon('music') + '<span data-i18n="generationSong">歌曲</span></button><button type="button" data-media-music-type="instrumental">' + icon('headphones') + '<span data-i18n="generationInstrumental">纯音乐 / 配乐</span></button></div></div>' +
            '<details id="mediaLyricsLabel" class="media-lyrics-editor" hidden><summary><span data-i18n="generationLyrics">歌词（可选）</span></summary><textarea id="mediaLyrics" rows="5" maxlength="3500" placeholder="填写已有歌词，或留空让平台创作" aria-label="歌词" data-i18n-placeholder="mediaLyricsPlaceholder"></textarea></details>' +
            '<div class="media-form-footer"><p class="media-form-hint" data-i18n="mediaBillingHint">按平台价格与生成结果结算</p><button id="mediaLogin" class="media-login" type="button" data-i18n="mediaSignIn">登录 SICurrent 账号</button><button id="mediaGenerate" class="media-generate" type="submit">生成图片</button><button id="mediaNewAttempt" class="media-login" type="button" hidden data-i18n="generationNewAttempt">已核对记录，开始新一次生成</button><p id="mediaFeedback" class="media-feedback" role="status" aria-live="polite"></p></div></form>' +
            '<div class="media-studio-output"><div class="media-output-heading"><h3 id="mediaOutputTitle">预览画布</h3><span data-i18n="mediaResults">生成结果</span></div><section id="mediaResult" class="media-result" aria-live="polite"></section><div class="media-history-heading"><h3 data-i18n="mediaRecent">最近生成</h3><button id="mediaRefresh" type="button">' + icon('refresh') + '<span data-i18n="mediaRefreshModels">刷新</span></button></div><div id="mediaHistory" class="media-history"></div></div></div></section>';
        document.body.appendChild(modal);
        localize();
        modal.querySelector('.close-modal').addEventListener('click', close);
        el('mediaMaximize').addEventListener('click', function () { window.toggleMacWindowSize?.(modal); });
        el('mediaTemplates').addEventListener('click', function () {
            if (submitting || kind === 'video' || typeof window.openPromptTemplates !== 'function') return;
            var targetKind = kind, targetAccount = lastUsername;
            window.openPromptTemplates({input: el('mediaPrompt'), mode: targetKind, isUsable: function () {
                return modal.isConnected && modal.style.display !== 'none' && !submitting && kind === targetKind && lastUsername === targetAccount;
            }});
        });
        modal.addEventListener('click', function (event) { if (event.target === modal) close(); });
        modal.addEventListener('keydown', function (event) {
            if (event.key === 'Escape') { event.preventDefault(); close(); }
            if (event.key === 'Tab') {
                var focusable = Array.from(modal.querySelectorAll('button:not(:disabled), select, textarea, a[href]')).filter(function (item) { return item.getClientRects().length; });
                var first = focusable[0], last = focusable[focusable.length - 1];
                if (event.shiftKey && event.target === first) { event.preventDefault(); last.focus(); }
                else if (!event.shiftKey && event.target === last) { event.preventDefault(); first.focus(); }
            }
        });
        document.addEventListener('keydown', function (event) {
            if (event.key === 'Escape' && !event.defaultPrevented && modal.style.display !== 'none' && !modal.classList.contains('mac-window-minimized')) {
                event.preventDefault(); close();
            }
        });
        el('mediaForm').addEventListener('submit', submit);
        el('mediaModel').addEventListener('change', updateForm);
        el('mediaCatalogRefresh').addEventListener('click', function () { load(el('mediaModel').value); });
        el('mediaMusicType').addEventListener('change', updateForm);
        el('mediaRatio').addEventListener('change', updateForm);
        modal.querySelectorAll('[data-media-ratio]').forEach(function (button) { button.addEventListener('click', function () { el('mediaRatio').value = button.dataset.mediaRatio; updateForm(); }); });
        modal.querySelectorAll('[data-media-music-type]').forEach(function (button) { button.addEventListener('click', function () { el('mediaMusicType').value = button.dataset.mediaMusicType; updateForm(); }); });
        el('mediaLogin').addEventListener('click', function () { if (typeof window.openAccountLogin === 'function') window.openAccountLogin(); });
        el('mediaRefresh').addEventListener('click', function () { stopReading(); history(sequence, true); });
        el('mediaNewAttempt').addEventListener('click', function () { uncertain = false; checkedUncertain = false; updateForm(); message('下一次点击生成将提交新的请求，可能产生新的费用。'); });
        modal.querySelectorAll('[data-media-kind]').forEach(function (button) {
            button.addEventListener('click', function () {
                if (submitting || button.dataset.mediaKind === kind) return;
                kind = button.dataset.mediaKind; el('mediaPrompt').value = ''; el('mediaLyrics').value = ''; load();
            });
        });
    }
    function localize() {
        modal.querySelectorAll('[data-i18n]').forEach(function (item) { item.textContent = t(item.dataset.i18n, item.textContent); });
        modal.querySelectorAll('[data-i18n-placeholder]').forEach(function (item) { item.placeholder = t(item.dataset.i18nPlaceholder, item.placeholder); });
    }
    document.addEventListener('openkitty:languagechange', function () {
        if (!modal) return;
        localize(); updateForm();
        if (!activeTask) renderEmpty();
    });
    window.openMediaStudio = function (capability, catalog, selected) {
        if (!modal) create();
        returnFocus = document.activeElement;
        kind = capability === 'music' ? 'music' : capability === 'video' ? 'video' : 'image';
        if (Array.isArray(catalog)) models = catalog;
        if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(modal);
        modal.style.display = 'flex';
        setModels(selected); load(selected); el('mediaPrompt').focus();
    };
    document.addEventListener('DOMContentLoaded', function () {
        var mode = 'agent'; try { mode = localStorage.getItem('macsInteractionMode') || mode; } catch (error) {}
        if (typeof window.setInteractionMode === 'function') window.setInteractionMode(mode);
    });
})();
