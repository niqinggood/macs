(function () {
    'use strict';
    var timers = new Map(), messages = new Map(), sending = false, configReady = true, accountSequence = 0, acknowledgedUnknown = new Set();
    var selectionSequence = 0;
    function t(key, fallback) { return window.openKittyT ? window.openKittyT(key, fallback) : fallback; }
    function el(id) { return document.getElementById(id); }
    function notify(text) { if (window.showToast) window.showToast(text, 'info'); }
    function escape(value) { return escapeHtml(String(value == null ? '' : value)); }
    function url(value) {
        try { var target = new URL(value, location.href); return ['https:', 'http:'].includes(target.protocol) && !target.username && !target.password ? target.href : ''; } catch (error) { return ''; }
    }
    function terminal(task) { return ['completed', 'failed', 'stopped', 'submission_unknown'].includes(task.status); }
    function errorText(code) {
        if(code==='reference_unavailable') return t('generationReferenceUnavailable','这张结果图暂不能直接引用，请下载后通过图片按钮上传。');
        if(['invalid_request','invalid_attachment','invalid_model','session_unavailable'].includes(code)) return t('generationRequestInvalid','请检查模型、参考图和参数，或切回当前工作区的对话。');
        var keys = {login_required:['generationLogin','请先登录 SICurrent 账号。'], invalid_image:['generationImageInvalid','参考图须为 JPG、PNG、WebP 或 GIF，且不超过 10MB。'], unsupported_attachments:['generationAttachmentsUnsupported','此模型不接受这些附件，请移除或切换模型。'], insufficient_funds:['generationInsufficientFunds','积分不足，请充值后再生成。'], invalid_generation:['generationParametersInvalid','模型不支持当前参数或参考图，请调整后重新发送。'], submission_unknown:['generationUnknown','提交结果尚未确认，请先核对生成记录，避免重复生成和扣费。'], upload_failed:['generationUploadFailed','参考图上传失败，尚未提交生成。'], request_conflict:['generationRequestConflict','该请求已保存，请重新打开对话核对结果。']};
        var item = keys[code] || ['generationUnavailable','暂时无法读取平台结果，请稍后刷新状态。']; return t(item[0], item[1]);
    }
    async function api(path, options) {
        var controller = new AbortController(), timeout = setTimeout(function () { controller.abort(); }, 90000);
        try {
            var response = await fetch(path, Object.assign({cache:'no-store', credentials:'same-origin', signal:controller.signal}, options));
            var data = await response.json();
            if (!response.ok || !data.ok) { var error = new Error(errorText(data.code)); error.code = data.code; error.status=response.status; throw error; }
            return data;
        } finally { clearTimeout(timeout); }
    }
    function createOptions() {
        if (el('generationOptions')) return;
        var options = document.createElement('div'); options.id='generationOptions'; options.className='composer-generation-options'; options.hidden=true;
        options.innerHTML='<div class="generation-options-row"><label id="generationRatioLabel"><span data-i18n="generationRatio">画面比例</span><select id="generationRatio"><option>1:1</option><option>16:9</option><option>9:16</option><option>4:3</option><option>3:4</option></select></label>' +
            '<label id="generationDurationLabel" hidden><span data-i18n="generationDuration">时长</span><select id="generationDuration"></select></label><label id="generationResolutionLabel" hidden><span data-i18n="generationResolution">清晰度</span><select id="generationResolution"></select></label>' +
            '<label id="generationMusicLabel" hidden><span data-i18n="generationMusicType">音乐类型</span><select id="generationMusicType"><option value="song" data-i18n="generationSong">歌曲</option><option value="instrumental" data-i18n="generationInstrumental">纯音乐 / 配乐</option></select></label><span id="generationPrice" class="generation-price"></span></div>' +
            '<details id="generationLyricsOptions" hidden><summary data-i18n="generationLyrics">歌词（可选）</summary><textarea id="generationLyrics" maxlength="3500" rows="3"></textarea></details><p id="generationInputHint" class="generation-input-hint" role="status" aria-live="polite" hidden></p>';
        el('messageInput').before(options);
        el('generationMusicType').addEventListener('change', updateComposer);
    }
    function setInputHint(text) {
        var hint=el('generationInputHint');hint.textContent=text;hint.hidden=!text;
    }
    function updateImageButton(reference) {
        var button=el('imageBtn'), key=reference?'generationReferenceTooltip':'uploadImage';
        button.dataset.i18nTitle=key;
        button.title=t(key,reference?'参考图 · 1 张 / 10MB（可粘贴或拖入）':'上传图片');
        button.setAttribute('aria-label',t(reference?'generationReferenceUpload':'uploadImage',reference?'上传参考图':'上传图片'));
    }
    function updateComposer() {
        createOptions();
        el('generationOptions').querySelectorAll('[data-i18n]').forEach(function(item){item.textContent=t(item.dataset.i18n,item.textContent);});
        el('generationDuration').querySelectorAll('option').forEach(function(option){option.textContent=option.value+' '+t('generationSeconds','秒');});
        var model=window.composerGenerationModel, active=!!model;
        window.updateInteractionModeHint?.();
        el('generationOptions').hidden=!active;
        var agent=el('agentSelector'); if(agent) agent.hidden=active;
        el('uploadBtn').hidden=active;
        el('imageBtn').hidden=active && model.capability==='music';
        updateImageButton(active && model.capability!=='music');
        if(!active) return;
        var image=model.capability==='image', video=model.capability==='video', music=model.capability==='music';
        el('generationRatioLabel').hidden=music;
        el('generationDurationLabel').hidden=!video; el('generationResolutionLabel').hidden=!video;
        el('generationMusicLabel').hidden=!music;
        el('generationLyricsOptions').hidden=!music || el('generationMusicType').value==='instrumental';
        var price=window.formatPlatformModelPrice(model);
        el('generationPrice').textContent=t('generationCost','费用')+'：'+price;
        el('messageInput').placeholder=t(image?'generationImagePrompt':video?'generationVideoPrompt':'generationMusicPrompt', image?'描述想生成的图片，也可以上传参考图':'描述想生成的内容');
        setInputHint(configReady?'':t('generationConfigLoading','正在读取视频参数，请稍候…'));
    }
    window.updateGenerationComposer=updateComposer;
    window.clearComposerGenerationModel=function () {
        selectionSequence++; window.composerGenerationModel=null; configReady=true;
        if(el('generationOptions')) el('generationOptions').hidden=true;
        if(el('agentSelector')) el('agentSelector').hidden=false;
        el('uploadBtn').hidden=false; el('imageBtn').hidden=false;
        updateImageButton(false);
        if(window.composerInteractionMode==='chat') el('messageInput').placeholder=t('generationChatPrompt','输入问题，模型直接回答（不执行工具）');
        window.updateComposerModelAgent?.();
        window.updateInteractionModeHint?.();
    };
    window.selectComposerGenerationModel=async function (model) {
        if(sending || window.composerModelSelectionPending || window.composerLoginCheckPending || typeof getActiveTaskForSession==='function' && getActiveTaskForSession(currentSessionId)) { notify(t('generationWait','请等待当前请求完成后再切换模型。')); return false; }
        window.setInteractionMode('chat');
        if(window.composerInteractionMode!=='chat') return false;
        window.composerGenerationModel=Object.assign({},model,{source:'platform'});
        var token=++selectionSequence; configReady=model.capability!=='video'; updateComposer(); window.updateComposerModelAgent?.(); el('messageInput').focus();
        if(model.capability==='video') {
            try {
                var result=await api('/api/platform/media/config');
                if(token!==selectionSequence) return;
                var capabilities=result.videoCapabilities;
                if(!capabilities || !Array.isArray(capabilities.durationOptions) || !Array.isArray(capabilities.resolutions)) throw new Error(t('generationUnavailable','无法读取视频参数'));
                var durations=el('generationDuration'), resolutions=el('generationResolution'); durations.replaceChildren(); resolutions.replaceChildren();
                capabilities.durationOptions.forEach(function(value){var option=document.createElement('option');option.value=String(value);option.textContent=value+' '+t('generationSeconds','秒');durations.appendChild(option);});
                durations.value=String(capabilities.defaultDuration);
                capabilities.resolutions.forEach(function(value){var option=document.createElement('option');option.value=value;option.textContent=value;resolutions.appendChild(option);});
                window.composerGenerationModel.supports_reference=!!capabilities.supportsSingleImage;
                window.composerGenerationModel.supports_text=!!capabilities.supportsTextToVideo;
                configReady=true; updateComposer();
            } catch(error) { if(token===selectionSequence){setInputHint(error.message+' '+t('generationSelectAgain','登录后重新选择此模型即可重试。'));} }
        }
        return token===selectionSequence;
    };
    window.acceptGenerationAttachment=function(file,pending) {
        var model=window.composerGenerationModel;
        if(!model) return true;
        if(model.capability==='music' || model.supports_reference===false) {notify(errorText('unsupported_attachments'));return false;}
        if(!['image/jpeg','image/png','image/webp','image/gif'].includes(file.type) || file.size>10*1024*1024) {notify(errorText('invalid_image'));return false;}
        if(pending.length>=1) {notify(t('generationOneReference','当前入口每次支持 1 张参考图，请先移除已有图片。'));return false;}
        return true;
    };
    function requestID() { var data=new Uint8Array(16);crypto.getRandomValues(data);return Array.from(data,function(v){return v.toString(16).padStart(2,'0');}).join(''); }
    window.sendGenerationMessage=async function () {
        if(sending || !isSessionEditable()) {notify(t('generationWait','请等待当前请求完成。'));return;}
        var model=Object.assign({},window.composerGenerationModel), prompt=el('messageInput').value.trim();
        if(Array.from(messages.values()).some(function(message){return message.session_id===currentSessionId && message.generation.capability===model.capability && ['submission_unknown','submitting'].includes(message.generation.task.status) && !message.generation.task.id && !acknowledgedUnknown.has(message.generation.request_id);})) {notify(errorText('submission_unknown'));return;}
        if(!prompt) {el('messageInput').focus();return;}
        try {
            if(!await window.requirePlatformAccountLogin()) return;
            if(!configReady && (!await window.selectComposerGenerationModel(model) || !configReady)) return;
        } catch(error) {notify(error.message);return;}
        var files;
        try { files=await window.getPendingMessageAttachments(); } catch(error) {notify(error.message);return;}
        if(sending) return;
        if(files.length>1 || files.some(function(file){return file.kind!=='image';}) || files.length && (model.capability==='music' || model.supports_reference===false) || !files.length && model.supports_text===false) {notify(errorText('unsupported_attachments'));return;}
        sending=true;
        try {
            if(!currentSessionId) await createNewSession();
            var session=currentSessionId, id=requestID(), identity=window.composerAccountIdentity;
            var payload={request_id:id,session_id:session,capability:model.capability,model:model.id,prompt:prompt,attachments:files,aspect_ratio:el('generationRatio').value};
            if(model.capability==='video') {payload.duration=Number(el('generationDuration').value);payload.resolution=el('generationResolution').value;}
            if(model.capability==='music') {payload.instrumental=el('generationMusicType').value==='instrumental';payload.lyrics=payload.instrumental?'':el('generationLyrics').value.trim();}
            clearConversationEmptyState(el('messagesArea')); el('messagesArea').insertAdjacentHTML('beforeend',renderMessage({role:'user',content:prompt,attachments:files,created_at:new Date().toISOString()}));
            var pending={role:'assistant',created_at:new Date().toISOString(),session_id:session,generation:{request_id:id,capability:model.capability,model:model.id,model_name:model.name,task:{status:'submitting',prompt:prompt}}};
            el('messagesArea').insertAdjacentHTML('beforeend',renderGenerationMessage(pending)); el('messageInput').value=''; window.clearPendingMessageAttachments(); scrollToBottom();
            var result=await api('/api/conversation/media',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(payload)});
            if(currentSessionId===session && identity===window.composerAccountIdentity) {replaceMessage(result.message); poll(result.message,session);}
            loadSessions();
        } catch(error) {
            notify(error.message || errorText('submission_unknown'));
            if(typeof id!=='undefined' && currentSessionId===session && identity===window.composerAccountIdentity) {
                pending.generation.task.status=error.status>=400 && error.status<500 ? 'failed' : 'submission_unknown';pending.generation.error_code=error.code||'submission_unknown';replaceMessage(pending);
                if(pending.generation.task.status==='failed') {el('messageInput').value=prompt;files.forEach(window.addReadyMessageAttachment);}
                // Only read the persisted snapshot after a lost response; never resend.
                try {var recovered=await api('/api/conversation/media/'+id);replaceMessage(recovered.message);poll(recovered.message,session);}catch(ignored){}
            }
        } finally {sending=false;updateComposer();}
    };
    function label(status) {
        var labels={submitting:['generationSubmitting','正在提交'],queued:['generationQueued','等待生成'],pending:['generationQueued','等待生成'],processing:['generationProcessing','正在生成'],running:['generationProcessing','正在生成'],completed:['generationCompleted','生成完成'],failed:['generationFailed','生成失败'],stopped:['generationStopped','已停止'],submission_unknown:['generationChecking','提交结果待核对']};
        var item=labels[status]||labels.submission_unknown;return t(item[0],item[1]);
    }
    function renderGenerationMessage(message) {
        var generation=message.generation, task=generation.task, id=generation.request_id, result=url(task.result_url), body='';
        if(task.status==='completed' && result) {
            if(generation.capability==='image') body='<img class="generation-result-image" src="'+escape(result)+'" alt="'+escape(task.prompt||'')+'" loading="lazy" referrerpolicy="no-referrer">';
            else if(generation.capability==='music') body='<div class="generation-music-card"><div class="generation-music-cover" aria-hidden="true"><div class="generation-music-disc"><span>♪</span></div></div><div class="generation-music-body"><span class="generation-music-caption">AI MUSIC</span><strong class="generation-music-title">'+escape(task.title||t('generationCompletedMusic','生成的音乐'))+'</strong><span class="generation-music-model">'+escape(generation.model_name)+'</span><audio class="generation-result-player" controls preload="none" aria-label="'+escape(task.title||t('composerTabMusic','音乐'))+'" src="'+escape(result)+'"></audio></div></div>';
            else body='<video class="generation-result-player" controls preload="none" src="'+escape(result)+'"></video>';
            body+='<div class="generation-result-actions"><a href="'+escape(result)+'" target="_blank" rel="noopener noreferrer">'+escape(t('generationDownload','打开 / 下载'))+'</a>';
            if(generation.capability==='image') body+='<button type="button" data-generation-reference="'+escape(id)+'">'+escape(t('generationUseReference','用作参考图 / 继续修改'))+'</button>';
            body+='</div>';
        } else if(!terminal(task)) body='<progress max="100" value="'+Math.max(0,Math.min(100,Number(task.progress)||0))+'" aria-label="'+escape(label(task.status))+'"></progress><p>'+escape(t('generationBackground','生成会在平台继续，重新打开对话可查看结果。'))+'</p>';
        if(generation.error_code) body+='<p class="generation-result-error">'+escape(errorText(generation.error_code))+'</p>';
        if(task.error_message) body+='<p class="generation-result-error">'+escape(task.error_message)+'</p>';
        if(task.lyrics) body+='<details class="generation-lyrics"><summary>'+escape(t('generationResultLyrics','歌词'))+'</summary><pre>'+escape(task.lyrics)+'</pre></details>';
        var billing={free:['settingsFree','免费'],reserved:['generationReserved','费用已预留'],charged:['generationCharged','已扣费'],settled:['generationCharged','已扣费'],refunded:['generationRefunded','已退款'],released:['generationReleased','预留已释放'],pending:['generationChecking','待核对']};
        var amount=typeof task.cost==='number'?task.cost:task.payment_status==='free'?0:null;
        var payment=billing[task.payment_status]||billing.pending;
        body+='<p class="generation-result-billing">'+escape(t('generationBilling','扣费状态'))+'：'+escape(t(payment[0],payment[1]))+(amount!==null?' · '+escape(amount)+' '+escape(t('generationPoints','积分')):'')+'</p>';
        if(task.status==='submission_unknown' || task.status==='submitting' && !task.id) body+='<div class="generation-result-actions"><button type="button" data-generation-history="'+escape(generation.capability)+'">'+escape(t('generationCheckHistory','核对平台生成记录'))+'</button><button type="button" data-generation-new-attempt="'+escape(id)+'">'+escape(t('generationNewAttempt','已核对记录，开始新一次生成'))+'</button></div>';
        if(task.id && !terminal(task)) body+='<button type="button" data-generation-refresh="'+escape(id)+'">'+escape(t('generationRefresh','刷新状态'))+'</button>';
        return '<div class="message assistant generation-message" data-generation-request="'+escape(id)+'" data-generation-title="'+escape(task.title||generation.model_name)+'"><div class="message-bubble"><div class="agent-name">'+escape(generation.model_name)+' <span class="composer-model-source platform">'+escape(t('composerSourcePlatform','平台'))+'</span></div><div class="message-content"><strong class="generation-result-status">'+escape(label(task.status))+'</strong>'+body+'</div><div class="message-info">'+escape(formatDate(message.created_at))+'</div></div></div>';
    }
    window.renderGenerationMessage=renderGenerationMessage;
    function replaceMessage(message) {
        var key=message.generation.request_id; messages.set(key,message);
        var target=document.querySelector('[data-generation-request="'+key+'"]');
        if(target) {var area=el('messagesArea'),follow=area.scrollHeight-area.scrollTop-area.clientHeight<120;target.outerHTML=renderGenerationMessage(message);if(follow)scrollToBottom();}
    }
    function poll(message,session) {
        var accountToken=accountSequence;
        var key=message.generation.request_id; clearTimeout(timers.get(key));timers.delete(key);
        if(terminal(message.generation.task) || !message.generation.task.id || currentSessionId!==session || document.hidden) return;
        timers.set(key,setTimeout(async function(){
            timers.delete(key);
            try {var result=await api('/api/conversation/media/'+key);if(currentSessionId!==session || accountToken!==accountSequence)return;replaceMessage(result.message);poll(result.message,session);}
            catch(error){if(currentSessionId===session){var target=document.querySelector('[data-generation-request="'+key+'"] .generation-result-status');if(target)target.textContent=error.message;}}
        },3000));
    }
    window.stopGenerationPolling=function(){timers.forEach(clearTimeout);timers.clear();messages.clear();};
    window.resumeGenerationMessages=function(list,session){window.stopGenerationPolling();list.forEach(function(message){if(message.generation){messages.set(message.generation.request_id,message);poll(message,session);}});};
    document.addEventListener('click',async function(event){
        var newAttempt=event.target.closest('[data-generation-new-attempt]');if(newAttempt){acknowledgedUnknown.add(newAttempt.dataset.generationNewAttempt);notify(t('generationNewAttemptHint','下一次发送将提交新的生成请求，可能产生新的费用。'));return;}
        var history=event.target.closest('[data-generation-history]');if(history){window.openMediaStudio(history.dataset.generationHistory);return;}
        var refresh=event.target.closest('[data-generation-refresh]');if(refresh){try{var result=await api('/api/conversation/media/'+refresh.dataset.generationRefresh);replaceMessage(result.message);poll(result.message,currentSessionId);}catch(error){notify(error.message);}return;}
        var reference=event.target.closest('[data-generation-reference]');if(reference){
            if(sending){notify(t('generationWait','请等待当前请求完成。'));return;}
            reference.disabled=true;
            try{
                var key=reference.dataset.generationReference, message=messages.get(key);
                var result=await api('/api/conversation/media/'+key+'/reference',{method:'POST'});
                if(message){var catalog=await api('/api/platform/models');var selected=catalog.models.find(function(model){return model.id===message.generation.model;});if(!selected)throw new Error(errorText('invalid_model'));if(!await window.selectComposerGenerationModel(selected))return;el('messageInput').value=message.generation.task.prompt||'';}
                window.clearPendingMessageAttachments();window.addReadyMessageAttachment(result.attachment);el('messageInput').focus();
            }catch(error){notify(error.message);}finally{reference.disabled=false;}
        }
    });
    document.addEventListener('visibilitychange',function(){if(!document.hidden)messages.forEach(function(message){poll(message,currentSessionId);});});
    document.addEventListener('openkitty:accountchange',function(){
        accountSequence++;window.stopGenerationPolling();
        if(currentSessionId && el('messagesArea').querySelector('.generation-message')) window.loadSession(currentSessionId);
        if(window.composerGenerationModel?.capability==='video' && !configReady && !sending) window.selectComposerGenerationModel(window.composerGenerationModel);
    });
    document.addEventListener('openkitty:languagechange',function(){updateComposer();messages.forEach(replaceMessage);});
})();
