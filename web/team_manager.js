(function () {
    'use strict';

    var COPY = {
        zh: {
            title: '项目专家团队', intro: '按任务选择团队，或添加自己的专家。成员职责和 Skill 保存到当前 macs 配置。',
            add: '新增团队', edit: '修改', use: '使用团队（Agent）', refresh: '刷新', search: '搜索团队或成员', builtin: '内置团队', custom: '我的团队',
            parallel: '并行协作', sequential: '按顺序协作', hybrid: '分阶段协作', members: '位成员',
            note: '使用后只填入对话框；请在 Agent 模式补充任务并发送。多位专家可能产生多次模型调用费用。',
            loading: '正在读取已保存的团队…', empty: '没有匹配的团队。可以新增一支团队。', loadFailed: '无法读取团队配置，请稍后刷新。',
            newTitle: '新增专家团队', editTitle: '修改专家团队', name: '团队名称', stable: '名称也是调用标识；保存后保持不变。',
            builtinStable: '内置调用标识固定，可以修改下面的成员职责和 Skill。', mode: '协作方式', parallelHelp: '自定义团队的成员并行工作，再汇总结果。', sequentialHelp: '成员依次执行，上一步的结果交给下一位。',
            locked: '这个团队使用固定阶段，成员数量和顺序保持不变。可以修改每位成员的名称、职责和 Skill。',
            role: '成员名称', prompt: '职责说明', skill: 'Skill（可选）', skillHint: '填写已安装 Skill 的目录名，如 ppt-art-director；留空不指定。',
            modelKept: '已配置的成员模型会保留。', addRole: '添加成员', removeRole: '移除成员', roleNumber: '成员',
            save: '保存团队', saving: '正在保存…', cancel: '取消', close: '关闭团队窗口', saved: '团队已保存，重新打开仍可使用。',
            failed: '保存失败，编辑内容已保留。', reloadFailed: '配置已保存，但运行时未更新。请检查服务日志后重试，编辑内容已保留。',
            conflict: '其他窗口更新了配置。请检查最新版本，再决定保留草稿或载入最新成员。', review: '检查最新配置',
            conflictReview: '已取得最新配置。保留草稿会在你再次点击保存时更新这支团队；其他配置不会被替换。',
            keepDraft: '保留草稿，使用新版本', loadLatest: '载入最新成员，替换草稿', latestMissing: '最新配置中没有这支团队。可以保留草稿作为新团队保存。',
            dirty: '尚有未保存的修改。', continueEdit: '继续编辑', discard: '放弃修改并关闭',
            invalidName: '团队名称需为 1–80 个字符，不能是 auto，也不能包含路径或控制字符。', duplicateName: '这个团队名称已存在，请换一个名称。',
            invalidRoles: '请填写 1–16 位成员，名称需为 1–80 个字符且不能重复，职责需为 1–8,000 个字符。全部职责最多 64,000 个字符。',
            invalidSkill: 'Skill 需是目录名（最多 128 个字符），仅含字母、数字、短横线、下划线和中间的点，不能包含路径。',
            inputMissing: '对话框暂不可用，请回到对话后再试。', readOnly: '当前会话只读，无法填入团队任务。原草稿已保留。',
            textModel: '请先选择文字模型，再使用专家团队。原草稿、附件和模型选择已保留。', agentBlocked: '当前任务仍在运行，暂不能切到 Agent 模式。请完成任务后再使用团队，原草稿已保留。',
            applied: '已追加团队说明。请在 Agent 模式补充任务后发送。', task: '请调用 assemble_team 工具，使用已保存的团队模板，template=',
            taskEnd: '。task 为本条消息中我提供的任务和材料。若任务尚未说明，请先询问我，不要自行编造任务。',
            researchName: '研究团队', researchDesc: '搜集资料、分析信息，再汇总研究报告。', codingName: '编码团队', codingDesc: '从架构设计到编码、审查，依次完成开发任务。',
            fullstackName: '全栈团队', fullstackDesc: '需求定义后，前后端协作开发，再进行测试。', contentName: '内容团队', contentDesc: '策划、写作与编辑共同完成内容。',
            pptName: 'PPT 工坊', pptDesc: '艺术总监、内容策划、设计排版与视觉审核，依次完成演示文稿。', codebaseName: '代码审计', codebaseDesc: '扫描项目、分析文件并审核架构。',
            contractName: '合同审查', contractDesc: '依次扫描结构、分析条款并审核合规风险。', competitorName: '竞对情报', competitorDesc: '扫描市场格局，并行分析竞对，再形成策略。',
            customDesc: '按你保存的成员职责协作。', details: '成员职责', configSource: '保存在当前 macs 配置，不依赖浏览器缓存。'
        },
        en: {
            title: 'Project expert teams', intro: 'Choose a team for your task or add your own experts. Responsibilities and Skills are saved in this macs configuration.',
            add: 'New team', edit: 'Edit', use: 'Use team (Agent)', refresh: 'Refresh', search: 'Search teams or members', builtin: 'Built-in', custom: 'My team',
            parallel: 'Parallel', sequential: 'Sequential', hybrid: 'Staged', members: 'members',
            note: 'This only adds instructions to your message. Complete the task and send in Agent mode. Multiple experts may incur multiple model calls.',
            loading: 'Loading saved teams…', empty: 'No matching teams. You can create a new team.', loadFailed: 'Could not load team configuration. Refresh to retry.',
            newTitle: 'New expert team', editTitle: 'Edit expert team', name: 'Team name', stable: 'The name is also the template ID and stays fixed after saving.',
            builtinStable: 'The built-in template ID stays fixed. Edit member responsibilities and Skills below.', mode: 'Collaboration', parallelHelp: 'Custom team members work in parallel, then combine their results.', sequentialHelp: 'Members work in sequence, passing each result to the next member.',
            locked: 'This team has fixed stages. Member count and order stay fixed; edit each member’s name, responsibilities and Skill.',
            role: 'Member name', prompt: 'Responsibilities', skill: 'Skill (optional)', skillHint: 'Use an installed Skill directory name, such as ppt-art-director. Leave blank for no specific Skill.',
            modelKept: 'Existing member model settings are retained.', addRole: 'Add member', removeRole: 'Remove member', roleNumber: 'Member',
            save: 'Save team', saving: 'Saving…', cancel: 'Cancel', close: 'Close team window', saved: 'Team saved. It will still be available when reopened.',
            failed: 'Save failed. Your editing draft is preserved.', reloadFailed: 'Configuration was saved, but the running service was not updated. Check the service logs and retry. Your draft is preserved.',
            conflict: 'Another window updated the configuration. Check the latest version, then keep your draft or load the latest members.', review: 'Check latest configuration',
            conflictReview: 'Latest configuration loaded. Keeping your draft updates this team only when you click Save again; other settings remain intact.',
            keepDraft: 'Keep draft with new version', loadLatest: 'Load latest members, replace draft', latestMissing: 'This team is not in the latest configuration. Keep the draft to save it as a new team.',
            dirty: 'You have unsaved changes.', continueEdit: 'Continue editing', discard: 'Discard changes and close',
            invalidName: 'Use 1–80 characters for the team name. Do not use auto, path separators or control characters.', duplicateName: 'This team name already exists. Choose another name.',
            invalidRoles: 'Provide 1–16 members with unique names of 1–80 characters and responsibilities of 1–8,000 characters. Total responsibilities must not exceed 64,000 characters.',
            invalidSkill: 'Use a Skill directory name of up to 128 characters containing letters, digits, hyphens, underscores and internal dots; no paths.',
            inputMissing: 'The message box is unavailable. Return to the conversation and try again.', readOnly: 'This conversation is read-only. Your existing draft is preserved.',
            textModel: 'Choose a text model before using an expert team. Your draft, attachments and model selection are preserved.', agentBlocked: 'The current task is still running, so Agent mode is unavailable. Finish the task, then choose the team again. Your draft is preserved.',
            applied: 'Team instructions appended. Complete your task and send in Agent mode.', task: 'Call the assemble_team tool with the saved team template, template=',
            taskEnd: '. The task is the work and materials I provide in this message. If I have not specified the task, ask me first; do not invent a task.',
            researchName: 'Research team', researchDesc: 'Gather sources, analyse information and combine a research report.', codingName: 'Coding team', codingDesc: 'Work through architecture, implementation and code review.',
            fullstackName: 'Full-stack team', fullstackDesc: 'Define requirements, develop frontend and backend together, then test.', contentName: 'Content team', contentDesc: 'Planning, writing and editing specialists work together.',
            pptName: 'Presentation studio', pptDesc: 'Art direction, content planning, layout and visual review in sequence.', codebaseName: 'Codebase audit', codebaseDesc: 'Scan the project, analyse files and review the architecture.',
            contractName: 'Contract review', contractDesc: 'Review structure, analyse clauses and assess compliance risks.', competitorName: 'Competitor intelligence', competitorDesc: 'Scan the market, analyse competitors in parallel and develop strategy.',
            customDesc: 'Collaborate using your saved member responsibilities.', details: 'Member responsibilities', configSource: 'Saved in this macs configuration, independently of browser storage.'
        }
    };
    var builtins = new Set(['research', 'coding', 'fullstack', 'content', 'ppt', 'codebase', 'contract', 'competitor']);
    var teams = [], revision = '', loaded = false, loading = false, query = '', notice = '', noticeExtra = '', library, editor;
    var loadSerial = 0, draft = null, baseline = '', draftRevision = '', editorNotice = '', editorExtra = '', closing = false, saving = false, latest = null, conflictPending = false;
    var keyPrefix = 'teamManager';

    function language() { return String(window.openKittyLanguage || document.documentElement.lang || 'zh').toLowerCase().indexOf('en') === 0 ? 'en' : 'zh'; }
    function t(key) { var fallback = COPY[language()][key] || COPY.zh[key] || key; return typeof window.openKittyT === 'function' ? window.openKittyT(keyPrefix + key.charAt(0).toUpperCase() + key.slice(1), fallback) : fallback; }
    function node(tag, cls, text) { var item = document.createElement(tag); if (cls) item.className = cls; if (text != null) item.textContent = text; return item; }
    function button(text, handler, cls) { var item = node('button', cls || 'tm-button', text); item.type = 'button'; item.addEventListener('click', handler); return item; }
    function count(text) { return Array.from(text).length; }
    function cloneRoles(roles) { return roles.map(function (role) { var next = {role: role.role, system_prompt: role.system_prompt, skill: role.skill || ''}; if (typeof role.model === 'string' && role.model) next.model = role.model; return next; }); }
    function displayName(team) { return team.builtin && builtins.has(team.id) ? t(team.id + 'Name') : team.name || team.id; }
    function description(team) { return team.builtin && builtins.has(team.id) ? t(team.id + 'Desc') : t('customDesc'); }
    function visible(view) { return view && view.overlay.classList.contains('active') && !view.overlay.classList.contains('mac-window-minimized'); }
    function topView() { return [editor, library].find(visible); }
    function focusable(view) { return Array.from(view.surface.querySelectorAll('button,input,textarea,select,[tabindex="0"]')).filter(function (item) { return !item.disabled && item.getClientRects().length; }); }
    function createView(id, cls, onClose) {
        var overlay = node('div', 'modal team-manager-modal ' + cls); overlay.id = id; overlay.setAttribute('role', 'dialog'); overlay.setAttribute('aria-modal', 'true'); overlay.setAttribute('aria-hidden', 'true');
        var surface = node('div', 'modal-content tm-surface'); var header = node('header', 'modal-header'); var title = node('h2'); title.id = id + 'Title'; overlay.setAttribute('aria-labelledby', title.id); header.appendChild(title);
        var close = button('×', onClose, 'close-modal'); close.setAttribute('aria-label', t('close')); header.appendChild(close);
        var view = {overlay: overlay, surface: surface, title: title, body: node('div', 'tm-body'), footer: node('footer', 'tm-footer'), focus: null};
        surface.append(header, view.body, view.footer); overlay.appendChild(surface); overlay.addEventListener('click', function (event) { if (event.target === overlay && topView() === view) onClose(); }); document.body.appendChild(overlay); return view;
    }
    function openView(view) {
        if (!view.overlay.classList.contains('active')) view.focus = document.activeElement;
        if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(view.overlay);
        view.overlay.classList.add('active'); view.overlay.setAttribute('aria-hidden', 'false'); var target = focusable(view)[0]; if (target) target.focus({preventScroll: true});
    }
    function closeView(view) {
        if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(view.overlay);
        view.overlay.classList.remove('active'); view.overlay.setAttribute('aria-hidden', 'true');
        var remaining = topView(), target = view.focus;
        if (!target || !target.isConnected || !target.getClientRects().length || remaining && !remaining.overlay.contains(target)) target = remaining ? focusable(remaining)[0] : document.getElementById('messageInput');
        if (target) target.focus({preventScroll: true});
    }
    function ensure() {
        if (library) return;
        var old = document.getElementById('teamOverlay'); if (old) old.remove();
        library = createView('teamOverlay', 'tm-library', function () { window.closeTeamPanel(); });
        editor = createView('teamTemplateEditorModal', 'tm-editor', requestEditorClose);
    }
    function validateResponse(data) {
        if (!data || !Array.isArray(data.teams) || typeof data.config_revision !== 'string' || !data.config_revision || new Set(data.teams.map(function (team) { return team.id; })).size !== data.teams.length) throw new Error(t('loadFailed'));
        data.teams.forEach(function (team) { if (!team || typeof team.id !== 'string' || !Array.isArray(team.roles) || !team.roles.length || !team.roles.every(function (role) { return role && typeof role.role === 'string' && typeof role.system_prompt === 'string'; })) throw new Error(t('loadFailed')); });
        return data;
    }
    async function request(method, body) {
        var options = {method: method, credentials: 'same-origin', cache: 'no-store', headers: {Accept: 'application/json'}};
        if (body) { options.headers['Content-Type'] = 'application/json'; options.body = JSON.stringify(body); }
        var response = await fetch('/api/team-templates', options), data;
        try { data = await response.json(); } catch (error) { throw new Error(method === 'GET' ? t('loadFailed') : t('failed')); }
        if (!response.ok || method === 'PUT' && data.status !== 'success') { var failure = new Error(typeof data.message === 'string' ? data.message : ''); failure.httpStatus = response.status; failure.data = data; throw failure; }
        return data;
    }
    async function loadTeams() {
        var serial = ++loadSerial; loading = true; loaded = false; notice = ''; noticeExtra = ''; renderLibrary();
        try { var data = validateResponse(await request('GET')); if (serial !== loadSerial) return false; teams = data.teams; revision = data.config_revision; loaded = true; }
        catch (error) { if (serial !== loadSerial) return false; notice = 'loadFailed'; }
        finally { if (serial === loadSerial) { loading = false; renderLibrary(); } }
        return loaded;
    }
    function renderLibrary() {
        if (!library) return;
        library.title.textContent = t('title'); library.body.replaceChildren(); library.footer.replaceChildren();
        var intro = node('p', 'tm-intro', t('intro')); var toolbar = node('div', 'tm-toolbar'); var search = node('input', 'tm-search'); search.type = 'search'; search.value = query; search.placeholder = t('search'); search.setAttribute('aria-label', t('search'));
        search.addEventListener('input', function () { query = search.value; renderCards(); });
        var refresh = button(t('refresh'), function () { loadTeams(); }); refresh.disabled = loading;
        var add = button(t('add'), function () { openEditor(); }, 'tm-button tm-primary'); add.id = 'teamManagerAdd'; add.disabled = !loaded || loading;
        toolbar.append(search, refresh, add); library.body.append(intro, toolbar); var feedback = node('p', 'tm-notice'); feedback.id = 'teamManagerNotice'; feedback.setAttribute('role', 'status'); feedback.hidden = !notice && !loading; feedback.textContent = loading ? t('loading') : t(notice) + (noticeExtra ? ' ' + noticeExtra : ''); library.body.appendChild(feedback);
        var grid = node('div', 'tm-grid'); grid.id = 'teamManagerGrid'; library.body.appendChild(grid); renderCards(); library.footer.append(node('p', 'tm-note', t('note')));
        library.overlay.querySelector('.close-modal').setAttribute('aria-label', t('close'));
    }
    function renderCards() {
        var grid = document.getElementById('teamManagerGrid'); if (!grid) return; grid.replaceChildren(); if (!loaded || loading) return;
        var needle = query.trim().toLocaleLowerCase(); var list = teams.filter(function (team) { return !needle || [team.id, displayName(team), description(team)].concat(team.roles.map(function (role) { return role.role; })).join(' ').toLocaleLowerCase().indexOf(needle) >= 0; });
        if (!list.length) { grid.appendChild(node('p', 'tm-empty', t('empty'))); return; }
        list.forEach(function (team) {
            var card = node('article', 'tm-card'); card.dataset.teamId = team.id;
            var meta = node('div', 'tm-card-meta'); meta.append(node('span', 'tm-source', t(team.builtin ? 'builtin' : 'custom')), node('span', 'tm-mode', t(team.mode || 'parallel')));
            var title = node('h3', '', displayName(team)); var desc = node('p', 'tm-card-desc', description(team)); var roles = node('div', 'tm-role-tags');
            team.roles.forEach(function (role) { var tag = node('span', '', role.role); tag.title = role.system_prompt; roles.appendChild(tag); });
            var actions = node('div', 'tm-card-actions'); var use = button(t('use'), function () { window.useTeamTemplate(team.id); }, 'tm-button tm-use'); use.dataset.teamUse = team.id;
            var edit = button(t('edit'), function () { openEditor(team.id); }); edit.dataset.teamEdit = team.id; edit.setAttribute('aria-label', t('edit') + ': ' + displayName(team)); actions.append(use, edit);
            card.append(meta, title, desc, roles, node('p', 'tm-member-count', team.roles.length + ' ' + t('members')), actions); grid.appendChild(card);
        });
    }
    function signature() { return draft ? JSON.stringify({id: draft.id, roles: draft.roles}) : ''; }
    function openEditor(id) {
        if (!loaded || loading || saving) return;
        if (editor.overlay.classList.contains('active') && draft) { openView(editor); return; }
        var team = id ? teams.find(function (item) { return item.id === id; }) : null; if (id && !team) return;
        draft = {id: team ? team.id : '', originalID: team ? team.id : '', builtin: !!(team && team.builtin), mode: team ? team.mode : 'parallel', locked: !!(team && team.role_count_locked), roles: team ? cloneRoles(team.roles) : [{role: '', system_prompt: '', skill: ''}]};
        draftRevision = revision; baseline = signature(); editorNotice = ''; editorExtra = ''; closing = false; latest = null; conflictPending = false; renderEditor(); openView(editor);
    }
    function field(labelText, value, handler, options) {
        options = options || {}; var label = node('label', 'tm-field'); label.appendChild(node('span', '', labelText)); var control = node(options.multiline ? 'textarea' : 'input'); if (!options.multiline) control.type = 'text'; else control.rows = 4;
        control.value = value; if (options.maxLength) control.maxLength = options.maxLength; control.readOnly = !!options.readOnly; control.disabled = saving; if (options.name) control.dataset.teamField = options.name;
        control.addEventListener('input', function () { handler(control.value); closing = false; if (editorNotice && !conflictPending) { editorNotice = ''; editorExtra = ''; } renderEditorFooter(); }); label.appendChild(control); return label;
    }
    function renderEditor() {
        if (!draft) return;
        editor.title.textContent = t(draft.originalID ? 'editTitle' : 'newTitle'); editor.body.replaceChildren(); editor.footer.replaceChildren();
        var overview = node('div', 'tm-editor-overview'); var name = field(t('name'), draft.id, function (value) {
            draft.id = value;
            if (!draft.originalID) { draft.mode = value.trim() === 'academic' ? 'sequential' : 'parallel'; var display = editor.body.querySelector('.tm-mode-info strong'); if (display) display.textContent = t(draft.mode); var hint = document.getElementById('teamNewModeHelp'); if (hint) hint.textContent = t(draft.mode === 'sequential' ? 'sequentialHelp' : 'parallelHelp'); }
        }, {maxLength: 160, readOnly: !!draft.originalID, name: 'id'}); overview.appendChild(name);
        var mode = node('div', 'tm-mode-info'); mode.append(node('span', 'tm-label', t('mode')), node('strong', '', t(draft.mode || 'parallel'))); overview.appendChild(mode); editor.body.appendChild(overview);
        editor.body.appendChild(node('p', 'tm-guidance', t(draft.builtin ? 'builtinStable' : 'stable')));
        if (draft.locked) editor.body.appendChild(node('p', 'tm-guidance tm-locked', t('locked'))); else if (!draft.builtin) { var modeHelp = node('p', 'tm-guidance', t(draft.mode === 'sequential' ? 'sequentialHelp' : 'parallelHelp')); modeHelp.id = 'teamNewModeHelp'; editor.body.appendChild(modeHelp); }
        var roleList = node('div', 'tm-editor-roles'); roleList.id = 'teamEditorRoles';
        draft.roles.forEach(function (role, index) {
            var item = node('section', 'tm-editor-role'); item.dataset.roleIndex = String(index); var header = node('div', 'tm-role-header'); header.appendChild(node('h3', '', t('roleNumber') + ' ' + (index + 1)));
            if (!draft.locked) { var remove = button(t('removeRole'), function () { if (saving || draft.roles.length <= 1) return; draft.roles.splice(index, 1); closing = false; renderEditor(); }, 'tm-button tm-remove'); remove.disabled = saving || draft.roles.length <= 1; header.appendChild(remove); } item.appendChild(header);
            item.appendChild(field(t('role'), role.role, function (value) { role.role = value; }, {maxLength: 160, name: 'role'}));
            item.appendChild(field(t('prompt'), role.system_prompt, function (value) { role.system_prompt = value; }, {multiline: true, maxLength: 16000, name: 'system_prompt'}));
            item.appendChild(field(t('skill'), role.skill, function (value) { role.skill = value; }, {maxLength: 256, name: 'skill'}));
            item.appendChild(node('p', 'tm-guidance', t('skillHint'))); if (role.model) item.appendChild(node('p', 'tm-guidance', t('modelKept'))); roleList.appendChild(item);
        }); editor.body.appendChild(roleList);
        if (!draft.locked) { var add = button(t('addRole'), function () { if (saving || draft.roles.length >= 16) return; draft.roles.push({role: '', system_prompt: '', skill: ''}); closing = false; renderEditor(); var fields = editor.body.querySelectorAll('[data-team-field="role"]'); fields[fields.length - 1].focus(); }, 'tm-button tm-add-role'); add.id = 'teamEditorAddRole'; add.disabled = saving || draft.roles.length >= 16; editor.body.appendChild(add); }
        editor.body.appendChild(node('p', 'tm-guidance tm-config-source', t('configSource'))); renderEditorFooter(); editor.overlay.querySelector('.close-modal').setAttribute('aria-label', t('close'));
    }
    function renderEditorFooter() {
        if (!draft) return; editor.footer.replaceChildren();
        var feedback = node('p', 'tm-notice'); feedback.id = 'teamEditorNotice'; feedback.setAttribute('role', 'status'); feedback.hidden = !editorNotice; feedback.textContent = editorNotice ? t(editorNotice) + (editorExtra ? ' ' + editorExtra : '') : ''; editor.footer.appendChild(feedback);
        var actions = node('div', 'tm-editor-actions');
        if (closing) {
            feedback.hidden = false; feedback.textContent = t('dirty'); actions.append(button(t('continueEdit'), function () { closing = false; renderEditorFooter(); }), button(t('discard'), function () { draft = null; closing = false; closeView(editor); }, 'tm-button tm-danger'));
        } else if (latest) {
            actions.append(button(t('keepDraft'), function () { draftRevision = latest.config_revision; revision = latest.config_revision; teams = latest.teams; if (!draft.originalID) draft.originalID = teams.some(function (team) { return team.id === draft.id.trim(); }) ? draft.id.trim() : ''; latest = null; conflictPending = false; editorNotice = ''; editorExtra = ''; renderLibrary(); renderEditor(); }, 'tm-button tm-primary'));
            var current = latest.teams.find(function (team) { return team.id === (draft.originalID || draft.id.trim()); });
            if (current) actions.append(button(t('loadLatest'), function () { revision = latest.config_revision; teams = latest.teams; draft.roles = cloneRoles(current.roles); draft.mode = current.mode; draft.locked = !!current.role_count_locked; draft.builtin = !!current.builtin; draft.originalID = current.id; draft.id = current.id; draftRevision = revision; baseline = signature(); latest = null; conflictPending = false; editorNotice = ''; editorExtra = ''; renderLibrary(); renderEditor(); }));
            else editor.body.querySelector('.tm-config-source').textContent = t('latestMissing');
        } else {
            var cancel = button(t('cancel'), requestEditorClose); cancel.disabled = saving; actions.appendChild(cancel);
            if (conflictPending) { var check = button(t('review'), reviewConflict, 'tm-button tm-primary'); check.disabled = saving; actions.appendChild(check); }
            else { var save = button(t(saving ? 'saving' : 'save'), saveEditor, 'tm-button tm-primary'); save.id = 'teamEditorSave'; save.disabled = saving; actions.appendChild(save); }
        } editor.footer.appendChild(actions);
    }
    function validName(name, reserved) { return !!name && count(name) <= 80 && !/[\p{Cc}/\\<>:"|?*]/u.test(name) && name !== '.' && name !== '..' && (!reserved || name.toLowerCase() !== 'auto'); }
    function validateDraft() {
        var id = draft.id.trim(); if (!validName(id, true)) return 'invalidName'; if (!draft.originalID && teams.some(function (team) { return team.id === id; })) return 'duplicateName';
        var names = new Set(), total = 0; if (!draft.roles.length || draft.roles.length > 16) return 'invalidRoles';
        for (var i = 0; i < draft.roles.length; i++) { var role = draft.roles[i], name = role.role.trim(), prompt = role.system_prompt.trim(), skill = role.skill.trim();
            if (!validName(name) || names.has(name) || !prompt || count(prompt) > 8000 || prompt.indexOf('\u0000') >= 0) return 'invalidRoles'; names.add(name); total += count(prompt);
            if (skill && (count(skill) > 128 || !/^(?!\.)(?!.*\.$)[\p{L}\p{Nd}_.-]+$/u.test(skill))) return 'invalidSkill';
        } return total > 64000 ? 'invalidRoles' : '';
    }
    async function reviewConflict() {
        if (saving || !draft) return; saving = true; renderEditorFooter();
        try { latest = validateResponse(await request('GET')); editorNotice = 'conflictReview'; editorExtra = ''; }
        catch (error) { editorNotice = 'loadFailed'; }
        finally { saving = false; renderEditorFooter(); }
    }
    async function saveEditor() {
        if (saving || !draft || conflictPending) return; var invalid = validateDraft(); if (invalid) { editorNotice = invalid; renderEditorFooter(); return; }
        saving = true; closing = false; editorNotice = ''; editorExtra = ''; editor.body.querySelectorAll('input,textarea,button').forEach(function (item) { item.disabled = true; }); renderEditorFooter();
        var body = {id: draft.id.trim(), roles: draft.roles.map(function (role) { var next = {role: role.role.trim(), system_prompt: role.system_prompt.trim(), skill: role.skill.trim()}; if (role.model) next.model = role.model; return next; }), config_revision: draftRevision};
        try {
            var data = await request('PUT', body); if (!data.team || data.team.id !== body.id || typeof data.config_revision !== 'string' || !data.config_revision) throw new Error();
            revision = data.config_revision; var position = teams.findIndex(function (team) { return team.id === body.id; }); if (position < 0) teams.push(data.team); else teams[position] = data.team;
            notice = 'saved'; noticeExtra = ''; draft = null; closeView(editor); renderLibrary();
        } catch (error) {
            if (error.httpStatus === 409) { editorNotice = 'conflict'; conflictPending = true; }
            else if (error.data && error.data.status === 'saved_reload_failed') { editorNotice = 'reloadFailed'; if (typeof error.data.config_revision === 'string' && error.data.config_revision) draftRevision = error.data.config_revision; }
            else editorNotice = 'failed';
        } finally { saving = false; if (draft) { editor.body.querySelectorAll('input,textarea,button').forEach(function (item) { item.disabled = false; }); renderEditor(); } }
    }
    function requestEditorClose() {
        if (saving) return; if (draft && signature() !== baseline) { closing = true; renderEditorFooter(); var target = editor.footer.querySelector('button'); if (target) target.focus(); return; } draft = null; closeView(editor);
    }
    function showLibraryNotice(key) { notice = key; noticeExtra = ''; renderLibrary(); }
    window.openTeamPanel = async function () { ensure(); if (editor.overlay.classList.contains('active')) { openView(editor); return; } renderLibrary(); openView(library); await loadTeams(); };
    window.closeTeamPanel = function () { if (!library) return; if (editor.overlay.classList.contains('active')) { requestEditorClose(); return; } closeView(library); };
    window.openTeamTemplateEditor = function (id) { ensure(); openEditor(id); };
    window.useTeamTemplate = async function (id) {
        ensure(); if (!loaded || loading) { if (!await loadTeams()) return; } var team = teams.find(function (item) { return item.id === id; }); if (!team) { showLibraryNotice('loadFailed'); return; }
        var input = document.getElementById('messageInput'); if (!input) { showLibraryNotice('inputMissing'); return; }
        if (input.disabled || input.readOnly || typeof window.isSessionEditable === 'function' && !window.isSessionEditable()) { showLibraryNotice('readOnly'); return; }
        if (window.composerGenerationModel) { showLibraryNotice('textModel'); return; }
        if (window.composerInteractionMode !== 'agent' && typeof window.setInteractionMode === 'function') window.setInteractionMode('agent');
        if (window.composerInteractionMode !== 'agent') { showLibraryNotice('agentBlocked'); return; }
        if (typeof window.switchMode === 'function') window.switchMode('single');
        var instruction = t('task') + JSON.stringify(team.id) + t('taskEnd'); var current = input.value; input.value = current.length ? current + '\n\n' + instruction : instruction;
        input.dispatchEvent(new Event('input', {bubbles: true})); closeView(library); input.focus(); input.setSelectionRange(input.value.length, input.value.length); if (typeof window.showToast === 'function') window.showToast(t('applied'), 'success');
    };
    document.addEventListener('keydown', function (event) {
        var view = topView(); if (!view || event.isComposing) return;
        if (event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); if (view === editor) requestEditorClose(); else window.closeTeamPanel(); return; }
        if (event.key !== 'Tab') return;
        var items = focusable(view); var dock = document.querySelector('.mac-window-dock'); if (dock && !dock.hidden) items = items.concat(Array.from(dock.querySelectorAll('button')).filter(function (item) { return item.getClientRects().length; }));
        if (!items.length) return; var position = items.indexOf(document.activeElement);
        if (event.shiftKey && position <= 0) { event.preventDefault(); items[items.length - 1].focus(); } else if (!event.shiftKey && (position < 0 || position === items.length - 1)) { event.preventDefault(); items[0].focus(); }
    }, true);
    document.addEventListener('openkitty:languagechange', function () { if (library) renderLibrary(); if (draft) renderEditor(); });
})();
