(function () {
    'use strict';

    var catalog = {models: [], agents: [], default_model: '', config_revision: ''};
    var loading = false;
    var loadSequence = 0;
    var feedback = '';
    var activeAgentValue = '';
    var pickerCapability = 'text';
    var platformCatalog = {models: [], loading: false, failed: false, loaded: false, fetchedAt: 0};

    function t(key, fallback) {
        return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
    }
    function modelLabel(id) {
        var model = catalog.models.find(function (item) { return item.id === id; });
        var provided = model?.source === 'platform' && platformCatalog.models.find(function (item) { return item.id === model.model && item.capability === 'text'; });
        if (provided) return provided.name || provided.model || provided.id;
        return model ? (model.model || model.id) : id;
    }
    function priceLabel(model) {
        if (!model?.billing?.unit || model.billing.currency && model.billing.currency !== 'POINT') return t('composerPricePending', '价格待确认');
        if (model.billing.unit === 'free') return '0 ' + t('composerFreePoints', '积分（免费）');
        if (model.billing.unit === 'per_success' && typeof model.billing.price === 'number' && Number.isFinite(model.billing.price) && model.billing.price >= 0) {
            var text = !model.capability || model.capability === 'text';
            return model.billing.price + ' ' + t(text ? 'composerPointsPerModelCall' : 'composerPointsPerGeneration', text ? '积分/次调用' : '积分/次生成');
        }
        return t('composerPricePending', '价格待确认');
    }
    function appendPrice(content, model) {
        var label = priceLabel(model);
        var pending = label === t('composerPricePending', '价格待确认');
        var compact = pending ? label : t('composerModelCharge', '扣费') + ' ' + model.billing.price + ' ' + t('generationPoints', '积分');
        if (!pending && model.billing.unit === 'free') compact = t('composerModelCharge', '扣费') + ' 0 ' + t('composerFreePoints', '积分（免费）');
        var price = element('span', 'composer-model-price' + (pending ? ' pending' : ''), compact);
        price.title = label;
        if (model?.billing?.unit === 'per_success' && !pending) {
            price.title += ' · ' + t('composerChargeOnSuccess', '成功后扣费');
        }
        content.appendChild(price);
    }
    function sourceBadge(model) {
        var platform = model.source === 'platform';
        var badge = element('span', 'composer-model-source ' + (platform ? 'platform' : 'custom'),
            t(platform ? 'composerSourcePlatform' : 'composerSourceCustom', platform ? '平台' : '自定义'));
        badge.title = t(platform ? 'composerSourcePlatformHint' : 'composerSourceCustomHint',
            platform ? 'SICurrent 平台提供，使用账号余额扣费' : '自己的 API 或本地模型，不扣平台余额');
        return badge;
    }
    function element(tag, className, value) {
        var node = document.createElement(tag);
        if (className) node.className = className;
        if (value) node.textContent = value;
        return node;
    }
    function closePicker(focus) {
        var panel = document.getElementById('composerModelMenu');
        var button = document.getElementById('modelConfigBtn');
        if (panel) panel.hidden = true;
        button?.setAttribute('aria-expanded', 'false');
        if (focus) button?.focus();
    }
    function positionPicker() {
        var panel = document.getElementById('composerModelMenu');
        var button = document.getElementById('modelConfigBtn');
        if (!panel || panel.hidden || !button) return;
        var box = button.getBoundingClientRect();
        var width = Math.min(340, window.innerWidth - 24);
        panel.style.width = width + 'px';
        panel.style.left = Math.max(12, Math.min(box.left, window.innerWidth - width - 12)) + 'px';
        panel.style.bottom = Math.max(12, window.innerHeight - box.top + 8) + 'px';
        panel.style.maxHeight = Math.max(120, box.top - 24) + 'px';
    }
    function renderPicker() {
        var button = document.getElementById('modelConfigBtn');
        var label = document.getElementById('composerModelName');
        if (!button || !label) return;
        var media = window.composerGenerationModel;
        var effectiveID = catalog.default_model;
        label.textContent = media ? media.name || media.model || media.id : modelLabel(effectiveID) || t('composerChooseModel', '选择模型');
        var currentModel = media || catalog.models.find(function (model) { return model.id === effectiveID; });
        var currentSource = document.getElementById('composerModelSource');
        if (currentSource) {
            var badge = currentModel ? sourceBadge(currentModel) : null;
            currentSource.hidden = !badge;
            currentSource.className = badge ? badge.className : 'composer-model-source';
            currentSource.textContent = badge ? badge.textContent : '';
            currentSource.title = badge ? badge.title : '';
        }
        var agentSelect = document.getElementById('agentSelect');
        if (agentSelect && agentSelect.selectedOptions[0]?.dataset.action !== 'create-agent') activeAgentValue = agentSelect.value;
        var title = t('composerCurrentModel', '当前模型') + ': ' + label.textContent;
        if (currentModel) title += ' · ' + sourceBadge(currentModel).title;
        if (currentModel?.source === 'platform') {
            var pricedModel = media || platformCatalog.models.find(function(model) { return model.id === currentModel.model; });
            title += ' · ' + t('generationCost', '费用') + ': ' + priceLabel(pricedModel);
        }
        if (!media) title += '\n' + t('composerWebModelPriority', '当前对话的 Agent 和简单问答都使用所选模型');
        button.title = title;
        button.setAttribute('aria-label', title);
        button.setAttribute('aria-busy', window.composerModelSelectionPending ? 'true' : 'false');

        var panel = document.getElementById('composerModelMenu');
        if (!panel) return;
        panel.replaceChildren();
        panel.appendChild(element('strong', 'composer-model-heading', t('composerChooseModel', '选择模型')));
        var tabs = element('div', 'composer-model-tabs');
        tabs.setAttribute('role', 'tablist');
        tabs.setAttribute('aria-label', t('composerModelPurpose', '模型用途'));
        [
            ['text', 'composerTabText', '对话'],
            ['image', 'composerTabImage', '生图'],
            ['music', 'composerTabMusic', '音乐'],
            ['video', 'composerTabVideo', '视频']
        ].forEach(function (item) {
            var tab = element('button', '', t(item[1], item[2]));
            tab.type = 'button'; tab.id = 'composerModelTab-' + item[0];
            tab.dataset.modelCapability = item[0];
            tab.setAttribute('role', 'tab');
            tab.setAttribute('aria-controls', 'composerModelChoices');
            tab.setAttribute('aria-selected', String(pickerCapability === item[0]));
            tab.tabIndex = pickerCapability === item[0] ? 0 : -1;
            if (item[0] !== 'text' && platformCatalog.loaded) {
                var count = platformCatalog.models.filter(function (model) { return model.capability === item[0]; }).length;
                tab.appendChild(element('span', 'composer-model-tab-count', String(count)));
            }
            tab.addEventListener('click', function () {
                pickerCapability = item[0]; renderPicker();
                document.getElementById(tab.id)?.focus();
            });
            tab.addEventListener('keydown', function (event) {
                var keys = ['ArrowLeft', 'ArrowRight', 'Home', 'End'];
                if (!keys.includes(event.key)) return;
                event.preventDefault();
                var choices = ['text', 'image', 'music', 'video'];
                var index = choices.indexOf(pickerCapability);
                pickerCapability = choices[event.key === 'Home' ? 0 : event.key === 'End' ? choices.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : choices.length - 1)) % choices.length];
                renderPicker(); document.getElementById('composerModelTab-' + pickerCapability)?.focus();
            });
            tabs.appendChild(tab);
        });
        panel.appendChild(tabs);
        var choices = element('div', 'composer-model-choices');
        choices.id = 'composerModelChoices'; choices.setAttribute('role', 'tabpanel');
        choices.setAttribute('aria-labelledby', 'composerModelTab-' + pickerCapability);
        panel.appendChild(choices);
        if (pickerCapability !== 'text') {
            renderMediaCatalog(choices); positionPicker(); return;
        }
        panel = choices;
        var status = element('p', 'composer-model-status', feedback || (loading ? t('composerModelsLoading', '正在读取模型…') : ''));
        status.setAttribute('role', 'status');
        if (!status.textContent) status.hidden = true;
        panel.appendChild(status);

        ['platform', 'custom'].forEach(function (source) {
            var heading = element('div', 'composer-model-group', t(source === 'platform' ? 'composerPlatformModels' : 'composerCustomModels', source === 'platform' ? 'SICurrent 平台模型 · 账号扣费' : '自定义模型 · 自有 API'));
            if (source === 'platform') {
                var refresh = element('button', 'composer-platform-refresh', t('composerRefreshPlatform', '刷新'));
                refresh.type = 'button'; refresh.disabled = platformCatalog.loading || !!window.composerModelSelectionPending;
                refresh.addEventListener('click', function () { refreshPlatformCatalog(true); });
                heading.appendChild(refresh);
            }
            panel.appendChild(heading);
            var models = catalog.models.filter(function (model) {
                var provided = platformCatalog.models.find(function (item) { return item.id === model.model; });
                return model.source === source && (source !== 'platform' || !provided || provided.capability === 'text');
            });
            var textModels = platformCatalog.models.filter(function (model) { return model.capability === 'text'; });
            if (source === 'platform' && (platformCatalog.loading || platformCatalog.failed || !models.length && !textModels.length)) {
                panel.appendChild(element('p', 'composer-model-empty', t(platformCatalog.loading ? 'composerPlatformLoading' : platformCatalog.failed ? 'composerPlatformUnavailable' : 'composerPlatformPending', '平台尚未提供可选的对话模型。')));
            } else if (source === 'custom' && !models.length) panel.appendChild(element('p', 'composer-model-empty', t('composerNoCustomModels', '填写 API 地址、API Key 和模型名称，即可添加。')));
            models.forEach(function (model) {
                var option = element('button', 'composer-model-option');
                option.type = 'button';
                option.disabled = !model.available || !!window.composerModelSelectionPending;
                option.dataset.modelId = model.id;
                option.setAttribute('aria-pressed', model.id === catalog.default_model ? 'true' : 'false');
                var content = element('span', 'composer-model-option-content');
                var row = element('span', 'composer-model-option-title');
                var modelName = element('strong', '', modelLabel(model.id));modelName.title=modelName.textContent;row.appendChild(modelName);
                row.appendChild(sourceBadge(model));
                content.appendChild(row);
                var provided = source === 'platform' && textModels.find(function (item) { return item.id === model.model; });
                if (source === 'platform') appendPrice(row, provided);
                var detail = source === 'platform' ? t(model.account_auth ? 'composerPlatformConnectHint' : 'composerPlatformKeyHint', model.account_auth ? '登录后按账号扣费' : '按访问密钥所属账号扣费') : model.id;
                content.appendChild(element('small', '', detail + (model.available ? '' : ' · ' + t('settingsModelNotReady', '未就绪'))));
                option.appendChild(content);
                option.appendChild(element('span', 'composer-model-check', model.id === catalog.default_model ? '✓' : ''));
                option.addEventListener('click', function () { selectModel(model.id); });
                panel.appendChild(option);
            });
            if (source === 'platform') renderPlatformCatalog(panel, models);
        });
        panel.appendChild(element('p', 'composer-model-empty', t('composerWebModelPriority', '当前对话的 Agent 和简单问答都使用所选模型')));
        var actions = element('div', 'composer-model-actions');
        [
            ['composerAddCustomModel', '添加自定义模型', 'custom'],
            ['composerManageModels', '管理模型', 'manage']
        ].forEach(function (action) {
            var item = element('button', '', t(action[0], action[1]));
            item.type = 'button';
            item.addEventListener('click', async function () {
                closePicker(false);
                await openConfigModal('models');
                if (action[2] === 'custom') addModel();
            });
            actions.appendChild(item);
        });
        panel.appendChild(actions);
        positionPicker();
    }
    function renderMediaCatalog(panel) {
        var heading = element('div', 'composer-model-group', t('composerPlatformMediaModels', 'SICurrent 平台模型'));
        var refresh = element('button', 'composer-platform-refresh', t('composerRefreshPlatform', '刷新'));
        refresh.type = 'button'; refresh.disabled = platformCatalog.loading || !!window.composerModelSelectionPending;
        refresh.addEventListener('click', function () { refreshPlatformCatalog(true); });
        heading.appendChild(refresh); panel.appendChild(heading);
        panel.appendChild(element('p', 'composer-model-empty', t('composerMediaOpenHint', '选择型号，在当前对话中描述并生成。')));
        var available = platformCatalog.models.filter(function (model) { return model.capability === pickerCapability; });
        if (platformCatalog.loading || platformCatalog.failed || !available.length) {
            panel.appendChild(element('p', 'composer-model-empty', t(platformCatalog.loading ? 'composerPlatformLoading' : platformCatalog.failed ? 'composerMediaUnavailable' : 'composerMediaPending', '平台暂未提供此类模型。')));
        }
        available.forEach(function (model) {
            var option = element('button', 'composer-model-option composer-platform-option');
            option.type = 'button'; option.dataset.platformModelId = model.id;
            option.disabled = !model.available || !model.direct_generation || platformCatalog.loading || !!window.composerModelSelectionPending;
            var content = element('span', 'composer-model-option-content');
            var row = element('span', 'composer-model-option-title');
            var modelName = element('strong', '', model.name || model.model || model.id);modelName.title=modelName.textContent;row.appendChild(modelName);
            row.appendChild(sourceBadge(model)); content.appendChild(row);
            appendPrice(row, model);
            var action = !model.available ? t('settingsModelNotReady', '未就绪') : !model.direct_generation ? t('composerMediaNotReady', '暂未开通直接生成') : t('composerMediaOpen', '打开生成窗口');
            content.appendChild(element('small', '', action));
            option.appendChild(content); option.appendChild(element('span', 'composer-model-check', '›'));
            option.addEventListener('click', function () {
                closePicker(false);
                window.selectComposerGenerationModel(model);
            });
            panel.appendChild(option);
        });
    }
    function renderPlatformCatalog(panel, configured) {
            platformCatalog.models.filter(function (model) {
                return model.capability === 'text' && !configured.some(function (item) { return item.model === model.id && item.account_auth; });
            }).forEach(function (model) {
                var option = element('button', 'composer-model-option composer-platform-option');
                option.type = 'button'; option.dataset.platformModelId = model.id;
                option.disabled = !model.available || !!window.composerModelSelectionPending || !catalog.config_revision;
                var content = element('span', 'composer-model-option-content');
                var row = element('span', 'composer-model-option-title');
                var modelName = element('strong', '', model.name || model.model || model.id);modelName.title=modelName.textContent;row.appendChild(modelName);
                row.appendChild(sourceBadge(model)); content.appendChild(row);
                appendPrice(row, model);
                content.appendChild(element('small', '', t('composerPlatformConnectHint', '登录后按账号扣费')));
                option.appendChild(content); panel.appendChild(option);
                option.addEventListener('click', function () { selectModel(model.id, true); });
            });
    }
    async function refreshPlatformCatalog(force) {
        if (platformCatalog.loading || !force && platformCatalog.loaded && Date.now() - platformCatalog.fetchedAt < 60000) return;
        platformCatalog.loading = true; renderPicker();
        var controller = new AbortController(); var timeout = setTimeout(function () { controller.abort(); }, 15000);
        try {
            var response = await fetch('/api/platform/models', {cache: 'no-store', signal: controller.signal});
            var data = await response.json();
            if (!response.ok || !data.ok || !Array.isArray(data.models)) throw new Error('platform_unavailable');
            platformCatalog.models = data.models; platformCatalog.loaded = true; platformCatalog.failed = false; platformCatalog.fetchedAt = Date.now();
        } catch (error) { platformCatalog.failed = true; platformCatalog.models = []; platformCatalog.loaded = false; }
        finally { clearTimeout(timeout); platformCatalog.loading = false; renderPicker(); }
    }
    async function refreshModels() {
        var sequence = ++loadSequence;
        loading = true;
        renderPicker();
        try {
            var response = await fetch('/api/models', {cache: 'no-store'});
            if (!response.ok) throw new Error('catalog_unavailable');
            var result = await response.json();
            if (sequence !== loadSequence) return;
            catalog = result;
            catalog.models = Array.isArray(result.models) ? result.models : [];
            catalog.agents = Array.isArray(result.agents) ? result.agents : [];
            feedback = '';
        } catch (error) {
            if (sequence === loadSequence) feedback = t('composerModelsFailed', '模型列表读取失败，点击选择器可重试。');
        } finally {
            if (sequence === loadSequence) { loading = false; renderPicker(); }
        }
    }
    async function selectModel(id, platform) {
        if (window.composerModelSelectionPending || window.composerLoginCheckPending || !catalog.config_revision) return;
        if (!platform && id === catalog.default_model) { window.clearComposerGenerationModel?.(); closePicker(true); return; }
        window.composerModelSelectionPending = true;
        feedback = t('composerModelApplying', '正在应用模型…');
        renderPicker();
        try {
            var payload = {config_revision: catalog.config_revision};
            payload[platform ? 'platform_model' : 'default_model'] = id;
            var response = await fetch('/api/models', {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(payload)});
            var result = await response.json().catch(function () { return {}; });
            if (!response.ok || result.status !== 'success') {
                if (response.status === 409) {
                    await refreshModels();
                    throw new Error('conflict');
                }
                throw new Error(result.error || 'apply_failed');
            }
            await refreshModels();
            window.clearComposerGenerationModel?.();
            if (typeof loadAgents === 'function') await loadAgents();
            closePicker(true);
            if (typeof showToast === 'function') showToast(t('composerModelApplied', '模型选择已生效'), 'success');
        } catch (error) {
            if (platform && error.message !== 'conflict') await refreshPlatformCatalog(true);
            feedback = t(error.message === 'conflict' ? 'composerModelConflict' : error.message === 'platform_catalog_unavailable' ? 'composerPlatformUnavailable' : 'composerModelApplyFailed', '模型切换失败，请刷新后重试。');
        } finally {
            window.composerModelSelectionPending = false;
            renderPicker();
        }
    }
    function init() {
        var button = document.getElementById('modelConfigBtn');
        if (!button) return;
        var panel = element('div', 'composer-model-menu');
        panel.id = 'composerModelMenu';
        panel.hidden = true;
        panel.setAttribute('role', 'dialog');
        panel.setAttribute('aria-label', t('composerChooseModel', '选择模型'));
        document.body.appendChild(panel);
        button.addEventListener('click', async function () {
            if (!panel.hidden) { closePicker(true); return; }
            pickerCapability = window.composerGenerationModel?.capability || 'text'; renderPicker();
            panel.hidden = false;
            button.setAttribute('aria-expanded', 'true');
            positionPicker();
            await Promise.all([refreshModels(), refreshPlatformCatalog()]);
            if (!panel.hidden) panel.querySelector('button[aria-pressed="true"]')?.focus();
        });
        document.getElementById('agentSelect')?.addEventListener('change', async function (event) {
            var select = event.target;
            if (select.selectedOptions[0]?.dataset.action !== 'create-agent') { renderPicker(); return; }
            select.value = activeAgentValue;
            if (select.selectedIndex < 0) select.value = window._defaultAgentName || '';
            renderPicker();
            closePicker(false);
            try {
                await openConfigModal('agents');
                addAgent();
                document.getElementById('settingsItemEditorModal').dataset.composerCreateAgent = 'true';
                document.querySelector('#settingsItemEditorBody .agent-name')?.focus();
            } catch (error) {
                if (typeof showToast === 'function') showToast(t('composerAddAgentFailed', '无法打开 Agent 配置，请重试。'), 'error');
            }
        });
        document.addEventListener('click', function (event) {
            var path = event.composedPath();
            if (path.indexOf(panel) < 0 && path.indexOf(button) < 0) closePicker(false);
        });
        document.addEventListener('keydown', function (event) { if (event.key === 'Escape' && !panel.hidden) { event.preventDefault(); closePicker(true); } });
        window.addEventListener('resize', positionPicker);
        window.addEventListener('scroll', positionPicker, true);
        document.addEventListener('openkitty:languagechange', renderPicker);
        refreshModels();
        refreshPlatformCatalog();
    }
    window.formatPlatformModelPrice = priceLabel;
    window.refreshComposerModels = refreshModels;
    window.updateComposerModelAgent = renderPicker;
    // Only the safe registry identity is submitted. Credentials and upstream
    // addresses remain in the backend; accepted tasks freeze their selection.
    window.composerModelRequiresAccount = function (id) {
        var model = catalog.models.find(function (item) { return item.id === (id || catalog.default_model); });
        return model?.source === 'platform' && model.account_auth !== false;
    };
    window.resolveComposerModelSelection = async function () {
        if (window.composerModelSelectionPending) throw new Error(t('composerModelApplying', '正在应用模型…'));
        if (!catalog.default_model) await refreshModels();
        var model = catalog.models.find(function (item) { return item.id === catalog.default_model; });
        if (!model?.available) throw new Error(t('composerModelUnavailable', '请先选择可用模型，再发送消息。'));
        return model.id;
    };
    if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
    else init();
})();
