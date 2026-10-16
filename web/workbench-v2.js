(function () {
    'use strict';

    var STORAGE = {
        session: 'macs.v2.activeSession',
        agent: 'macs.v2.selectedAgent',
        user: 'userId'
    };
    var ACTIVE_STATUSES = { pending: true, running: true, waiting: true, waiting_approval: true };
    var TERMINAL_STATUSES = { completed: true, degraded: true, failed: true, stopped: true };
    var state = {
        sessions: [],
        tasks: [],
        artifacts: [],
        agents: [],
        skills: [],
        employees: [],
        messages: [],
        progressEvents: [],
        attachments: [],
        project: null,
        health: null,
        currentSessionId: '',
        currentTask: null,
        selectedAgent: '',
        taskError: null,
        followupAck: '',
        eventSource: null,
        pollTimer: null,
        refreshTimer: null,
        loadToken: 0,
        submitting: false,
        finishingTasks: Object.create(null)
    };
    var dom = {};

    document.addEventListener('DOMContentLoaded', init);

    function init() {
        cacheDOM();
        bindEvents();
        state.selectedAgent = storageGet(STORAGE.agent);
        state.currentSessionId = sessionFromURL() || storageGet(STORAGE.session);
        refreshIcons();
        refreshAll({ initial: true });
        state.refreshTimer = window.setInterval(refreshBackgroundState, 5000);
    }

    function cacheDOM() {
        [
            'workbenchShell', 'sidebar', 'mobileScrim', 'sidebarClose', 'mobileMenuButton',
            'newTaskButton', 'projectNewTaskButton', 'refreshSessionsButton', 'sessionList',
            'runningCount', 'workspaceName', 'projectButton', 'serviceStatus', 'searchButton',
            'topbarWorkspaceName',
            'artifactsButton', 'moreButton', 'moreMenu', 'welcomeView', 'conversationView',
            'taskTitle', 'taskSubtitle', 'taskState', 'taskMoreButton', 'messageTimeline',
            'timelineSentinel', 'composerDock', 'composerForm', 'taskInput', 'attachButton',
            'capabilityButton', 'modelButton', 'modelLabel', 'stopTaskButton', 'sendButton',
            'composerHint', 'attachmentStrip', 'welcomeExamples', 'readyLine', 'inspector',
            'artifactsTab', 'detailsTab', 'closeInspectorButton', 'artifactsPanel',
            'detailsPanel', 'artifactList', 'refreshArtifactsButton', 'detailList',
            'progressLogLink', 'fileInput', 'searchDialog', 'searchInput', 'searchResults',
            'capabilityDialog', 'agentSelect', 'agentHelp', 'employeeCount', 'employeeList',
            'skillCount', 'skillList', 'projectDialog', 'projectSummary', 'toastRegion'
        ].forEach(function (id) {
            dom[id] = document.getElementById(id);
        });
    }

    function bindEvents() {
        dom.newTaskButton.addEventListener('click', startNewTask);
        dom.projectNewTaskButton.addEventListener('click', startNewTask);
        dom.refreshSessionsButton.addEventListener('click', function () {
            refreshSessions(true);
        });
        dom.mobileMenuButton.addEventListener('click', openSidebar);
        dom.sidebarClose.addEventListener('click', collapseSidebar);
        dom.mobileScrim.addEventListener('click', closeSidebar);

        document.querySelectorAll('[data-nav]').forEach(function (button) {
            button.addEventListener('click', function () {
                handleNavigation(button.getAttribute('data-nav'));
            });
        });
        document.querySelectorAll('[data-mobile-nav]').forEach(function (button) {
            button.addEventListener('click', function () {
                handleMobileNavigation(button.getAttribute('data-mobile-nav'));
            });
        });
        document.querySelectorAll('[data-example]').forEach(function (button) {
            button.addEventListener('click', function () {
                dom.taskInput.value = button.getAttribute('data-example') || '';
                resizeInput();
                dom.taskInput.focus();
            });
        });
        document.querySelectorAll('[data-close-dialog]').forEach(function (button) {
            button.addEventListener('click', function () {
                closeDialog(document.getElementById(button.getAttribute('data-close-dialog')));
            });
        });

        dom.projectButton.addEventListener('click', function () { openDialog(dom.projectDialog); });
        dom.serviceStatus.addEventListener('click', describeServiceStatus);
        dom.searchButton.addEventListener('click', openSearch);
        dom.artifactsButton.addEventListener('click', function () { openInspector('artifacts'); });
        dom.taskMoreButton.addEventListener('click', function () { openInspector('details'); });
        dom.closeInspectorButton.addEventListener('click', closeInspector);
        dom.artifactsTab.addEventListener('click', function () { selectInspectorTab('artifacts'); });
        dom.detailsTab.addEventListener('click', function () { selectInspectorTab('details'); });
        dom.refreshArtifactsButton.addEventListener('click', function () { refreshArtifacts(true); });

        dom.moreButton.addEventListener('click', toggleMoreMenu);
        dom.moreMenu.addEventListener('click', function (event) {
            var action = event.target.closest('[data-menu-action]');
            if (action && action.getAttribute('data-menu-action') === 'refresh') {
                closeMoreMenu();
                refreshAll({ announce: true });
            }
        });

        dom.composerForm.addEventListener('submit', function (event) {
            event.preventDefault();
            submitComposer();
        });
        dom.taskInput.addEventListener('input', resizeInput);
        dom.taskInput.addEventListener('keydown', function (event) {
            if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
                event.preventDefault();
                submitComposer();
            }
        });
        dom.attachButton.addEventListener('click', function () { dom.fileInput.click(); });
        ['dragenter', 'dragover'].forEach(function (eventName) {
            dom.composerForm.addEventListener(eventName, function (event) {
                event.preventDefault();
                dom.composerForm.classList.add('is-dragging');
            });
        });
        ['dragleave', 'drop'].forEach(function (eventName) {
            dom.composerForm.addEventListener(eventName, function (event) {
                event.preventDefault();
                dom.composerForm.classList.remove('is-dragging');
            });
        });
        dom.composerForm.addEventListener('drop', function (event) {
            queueFiles(Array.prototype.slice.call((event.dataTransfer && event.dataTransfer.files) || []));
        });
        dom.fileInput.addEventListener('change', function () {
            queueFiles(Array.prototype.slice.call(dom.fileInput.files || []));
            dom.fileInput.value = '';
        });
        dom.capabilityButton.addEventListener('click', openCapabilities);
        dom.modelButton.addEventListener('click', openCapabilities);
        dom.agentSelect.addEventListener('change', selectAgent);
        dom.stopTaskButton.addEventListener('click', stopCurrentTask);

        dom.searchInput.addEventListener('input', renderSearchResults);
        dom.searchResults.addEventListener('click', function (event) {
            var result = event.target.closest('[data-session-id]');
            if (!result) return;
            closeDialog(dom.searchDialog);
            loadSession(result.getAttribute('data-session-id'));
        });
        dom.sessionList.addEventListener('click', function (event) {
            var item = event.target.closest('[data-session-id]');
            if (!item) return;
            loadSession(item.getAttribute('data-session-id'));
        });
        dom.attachmentStrip.addEventListener('click', function (event) {
            var button = event.target.closest('[data-remove-attachment]');
            if (button) removeAttachment(button.getAttribute('data-remove-attachment'));
        });
        dom.employeeList.addEventListener('click', function (event) {
            var button = event.target.closest('[data-mention]');
            if (button) chooseMention(button.getAttribute('data-mention'));
        });
        dom.skillList.addEventListener('click', function (event) {
            var button = event.target.closest('[data-mention]');
            if (button) chooseMention(button.getAttribute('data-mention'));
        });

        [dom.searchDialog, dom.capabilityDialog, dom.projectDialog].forEach(function (dialog) {
            dialog.addEventListener('click', function (event) {
                if (event.target === dialog) closeDialog(dialog);
            });
        });

        document.addEventListener('click', function (event) {
            if (!event.target.closest('.more-menu-wrap')) closeMoreMenu();
        });
        document.addEventListener('keydown', function (event) {
            if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
                event.preventDefault();
                openSearch();
            }
            if (event.key === 'Escape') {
                [dom.searchDialog, dom.capabilityDialog, dom.projectDialog].forEach(closeDialog);
                closeSidebar();
                closeMoreMenu();
            }
        });
        window.addEventListener('popstate', function () {
            var sessionID = sessionFromURL();
            if (sessionID) loadSession(sessionID, { updateURL: false });
            else startNewTask({ updateURL: false });
        });
        window.addEventListener('beforeunload', closeTaskSubscription);
    }

    async function refreshAll(options) {
        options = options || {};
        var requests = [
            getJSON('/api/health'),
            getJSON('/api/status'),
            getJSON('/api/agents'),
            getJSON('/api/sessions?limit=100'),
            getJSON('/api/tasks/running?include_history=1&limit=100'),
            getJSON('/api/artifacts?limit=100'),
            getJSON('/api/skills'),
            getJSON('/api/ae/list')
        ];
        var results = await Promise.allSettled(requests);

        if (results[0].status === 'fulfilled') state.health = results[0].value;
        if (results[1].status === 'fulfilled') state.project = results[1].value.project || null;
        if (results[2].status === 'fulfilled') {
            state.agents = results[2].value.agents || [];
            if (!state.selectedAgent) state.selectedAgent = results[2].value.default_agent || '';
        }
        if (results[3].status === 'fulfilled') state.sessions = results[3].value.sessions || [];
        if (results[4].status === 'fulfilled') state.tasks = flattenTasks(results[4].value);
        if (results[5].status === 'fulfilled') state.artifacts = results[5].value.artifacts || [];
        if (results[6].status === 'fulfilled') state.skills = results[6].value.skills || [];
        if (results[7].status === 'fulfilled') state.employees = results[7].value.employees || [];

        renderHealth();
        renderProject();
        renderAgents();
        renderCapabilities();
        renderSidebar();
        renderArtifacts();
        syncCurrentTask();

        if (options.initial) {
            var storedExists = state.sessions.some(function (session) {
                return session.id === state.currentSessionId;
            });
            if (state.currentSessionId && storedExists) {
                await loadSession(state.currentSessionId, { updateURL: false, closeSidebar: false });
            } else {
                startNewTask({ updateURL: false });
            }
        }
        if (options.announce) showToast('工作台已刷新', 'success');
    }

    async function refreshBackgroundState() {
        var results = await Promise.allSettled([
            getJSON('/api/health'),
            getJSON('/api/tasks/running?include_history=1&limit=100')
        ]);
        if (results[0].status === 'fulfilled') state.health = results[0].value;
        if (results[1].status === 'fulfilled') state.tasks = flattenTasks(results[1].value);
        renderHealth();
        renderSidebar();
        syncCurrentTask();
    }

    async function refreshSessions(announce) {
        try {
            var data = await getJSON('/api/sessions?limit=100');
            state.sessions = data.sessions || [];
            renderSidebar();
            renderSearchResults();
            if (announce) showToast('最近任务已更新', 'success');
        } catch (error) {
            showRequestError(error, '暂时无法刷新最近任务');
        }
    }

    async function refreshArtifacts(announce) {
        try {
            var data = await getJSON('/api/artifacts?limit=100');
            state.artifacts = data.artifacts || [];
            renderArtifacts();
            renderProject();
            if (announce) showToast('产物列表已更新', 'success');
        } catch (error) {
            showRequestError(error, '暂时无法刷新产物');
        }
    }

    function flattenTasks(data) {
        data = data || {};
        return []
            .concat(data.running || [])
            .concat(data.pending || [])
            .concat(data.completed || [])
            .concat(data.degraded || [])
            .concat(data.failed || [])
            .concat(data.stopped || []);
    }

    function startNewTask(options) {
        options = options || {};
        closeTaskSubscription();
        state.currentSessionId = '';
        state.currentTask = null;
        state.messages = [];
        state.progressEvents = [];
        if (!options.preserveError) state.taskError = null;
        state.followupAck = '';
        storageRemove(STORAGE.session);
        setView('welcome');
        closeSidebar();
        closeInspector();
        closeDialog(dom.projectDialog);
        dom.taskInput.value = '';
        dom.taskTitle.textContent = '新任务';
        dom.taskSubtitle.textContent = '在当前工作区中持续完成并保存成果。';
        updateTaskState('idle');
        dom.taskInput.placeholder = '描述任务，或拖入文件';
        resizeInput();
        updateComposerState();
        renderSidebar();
        renderDetails();
        if (options.updateURL !== false) updateSessionURL('');
        window.setTimeout(function () { dom.taskInput.focus(); }, 20);
    }

    async function loadSession(sessionID, options) {
        options = options || {};
        sessionID = String(sessionID || '').trim();
        if (!sessionID) {
            startNewTask(options);
            return;
        }

        var token = ++state.loadToken;
        state.currentSessionId = sessionID;
        storageSet(STORAGE.session, sessionID);
        if (!options.preserveError) state.taskError = null;
        state.followupAck = '';
        setView('conversation');
        renderSessionHeader();
        dom.messageTimeline.innerHTML = '<div class="loading-history">正在载入任务记录…</div>';
        if (options.closeSidebar !== false) closeSidebar();
        if (options.updateURL !== false) updateSessionURL(sessionID);
        renderSidebar();

        try {
            var data = await getJSON('/api/session/' + encodeURIComponent(sessionID) + '?limit=500');
            if (token !== state.loadToken || state.currentSessionId !== sessionID) return;
            state.messages = (data.messages || []).filter(function (message) {
                return message && message.role !== 'system';
            });
            state.currentTask = taskForSession(sessionID);
            var taskIsActive = state.currentTask &&
                ACTIVE_STATUSES[String(state.currentTask.status || '').toLowerCase()];
            state.progressEvents = taskIsActive ? state.progressEvents : [];
            renderSessionHeader();
            renderTimeline();
            renderDetails();
            updateComposerState();
            if (taskIsActive) subscribeCurrentTask(state.currentTask);
            scrollTimelineToBottom(false);
        } catch (error) {
            if (token !== state.loadToken) return;
            state.messages = [];
            state.taskError = requestErrorModel(error, '无法载入这个任务');
            renderTimeline();
            renderDetails();
        }
    }

    function setView(view) {
        dom.workbenchShell.setAttribute('data-view', view);
        var isConversation = view === 'conversation';
        dom.welcomeView.hidden = isConversation;
        dom.welcomeExamples.hidden = isConversation;
        dom.readyLine.hidden = isConversation;
        dom.conversationView.hidden = !isConversation;
    }

    function renderSidebar() {
        var running = state.tasks.filter(function (task) {
            return ACTIVE_STATUSES[String(task.status || '').toLowerCase()];
        });
        dom.runningCount.textContent = String(running.length);
        dom.runningCount.hidden = running.length === 0;

        if (!state.sessions.length) {
            dom.sessionList.innerHTML = '<div class="session-empty">还没有任务记录。<br>从上方新建任务开始。</div>';
            return;
        }

        dom.sessionList.innerHTML = state.sessions.slice(0, 40).map(function (session) {
            var task = activeTaskForSession(session.id);
            var title = sessionTitle(session);
            var active = session.id === state.currentSessionId ? ' is-active' : '';
            var statusText = task ? statusLabel(task.status) : relativeTime(session.updated_at);
            var dotClass = task ? ' is-running' : '';
            return '<button class="session-item' + active + '" type="button" data-session-id="' +
                escapeAttribute(session.id) + '"' + (active ? ' aria-current="page"' : '') + '>' +
                '<span class="session-icon"><i data-lucide="' + sessionIcon(title) + '" aria-hidden="true"></i></span>' +
                '<span class="session-copy"><span class="session-title">' + escapeHTML(title) + '</span>' +
                '<span class="session-meta"><span class="mini-dot' + dotClass + '"></span>' +
                '<span>' + escapeHTML(statusText) + '</span></span></span>' +
                '<span class="session-age">' + escapeHTML(shortAge(session.updated_at)) + '</span></button>';
        }).join('');
        refreshIcons();
    }

    function renderSessionHeader() {
        var session = sessionByID(state.currentSessionId);
        var title = session ? sessionTitle(session) : firstUserText() || '新任务';
        dom.taskTitle.textContent = truncateText(title, 42);
        dom.taskSubtitle.textContent = truncateText(firstUserText() || '在当前工作区中持续完成并保存成果。', 110);
        updateTaskState(state.currentTask ? state.currentTask.status : 'completed');
    }

    function renderTimeline() {
        dom.messageTimeline.innerHTML = '';
        if (!state.messages.length && !state.currentTask && !state.taskError) {
            dom.messageTimeline.innerHTML = '<div class="conversation-empty">这个任务还没有消息，可以从下方继续。</div>';
            return;
        }

        state.messages.forEach(function (message) {
            dom.messageTimeline.appendChild(buildMessage(message));
        });

        if (state.currentTask && ACTIVE_STATUSES[String(state.currentTask.status || '').toLowerCase()]) {
            dom.messageTimeline.appendChild(buildProgressTimeline());
        }
        if (state.followupAck) {
            var ack = document.createElement('div');
            ack.className = 'followup-ack';
            ack.innerHTML = '<i data-lucide="circle-check" aria-hidden="true"></i><span>' +
                escapeHTML(state.followupAck) + '</span>';
            dom.messageTimeline.appendChild(ack);
        }
        if (state.taskError) dom.messageTimeline.appendChild(buildErrorCard(state.taskError));
        refreshIcons();
    }

    function buildMessage(message) {
        var article = document.createElement('article');
        var isUser = message.role === 'user';
        article.className = 'timeline-message ' + (isUser ? 'is-user' : 'is-assistant');
        var role = document.createElement('div');
        role.className = 'message-role';
        if (!isUser) {
            var mark = document.createElement('span');
            mark.className = 'agent-mark';
            mark.innerHTML = '<i data-lucide="sparkles" aria-hidden="true"></i>';
            role.appendChild(mark);
        }
        var name = document.createElement('span');
        name.textContent = isUser ? '你' : (message.agent_name || 'MACS');
        var time = document.createElement('time');
        time.className = 'message-time';
        time.dateTime = message.created_at || '';
        time.textContent = formatClock(message.created_at);
        role.appendChild(name);
        role.appendChild(time);
        article.appendChild(role);

        var content = document.createElement('div');
        content.className = 'message-content';
        content.innerHTML = renderMarkdown(message.content || '');
        article.appendChild(content);

        if (message.attachments && message.attachments.length) {
            var attachments = document.createElement('div');
            attachments.className = 'message-attachments';
            message.attachments.forEach(function (item) {
                var row = document.createElement('div');
                row.className = 'message-attachment';
                row.innerHTML = '<i data-lucide="paperclip" aria-hidden="true"></i><span>' +
                    escapeHTML(item.name || item.path || '附件') + '</span>';
                attachments.appendChild(row);
            });
            article.appendChild(attachments);
        }
        return article;
    }

    function buildProgressTimeline() {
        var section = document.createElement('details');
        section.className = 'progress-timeline-v2';
        section.setAttribute('aria-label', '任务执行进度');
        var events = state.progressEvents.length ? state.progressEvents : [{
            title: state.currentTask && state.currentTask.status === 'pending' ? '任务正在排队' : '任务正在运行',
            detail: 'MACS 会在这里持续更新关键步骤。',
            state: 'running',
            timestamp: new Date().toISOString()
        }];
        var summary = document.createElement('summary');
        summary.innerHTML = '<span class="progress-summary-icon"><i data-lucide="activity" aria-hidden="true"></i></span>' +
            '<span>正在执行 · ' + events.length + ' 个步骤</span>' +
            '<span class="progress-summary-state">查看过程</span>' +
            '<i class="progress-summary-chevron" data-lucide="chevron-down" aria-hidden="true"></i>';
        section.appendChild(summary);
        var list = document.createElement('div');
        list.className = 'progress-event-list';
        events.slice(-10).forEach(function (event) {
            var row = document.createElement('div');
            row.className = 'progress-row is-' + (event.state || 'running');
            row.innerHTML = '<span class="progress-marker" aria-hidden="true"></span>' +
                '<div><div class="progress-title">' + escapeHTML(event.title || '正在处理') + '</div>' +
                (event.detail ? '<div class="progress-copy">' + escapeHTML(event.detail) + '</div>' : '') +
                '</div><time class="progress-time">' + escapeHTML(formatClock(event.timestamp)) + '</time>';
            list.appendChild(row);
        });
        section.appendChild(list);
        return section;
    }

    function buildErrorCard(model) {
        var card = document.createElement('section');
        card.className = 'task-error-card';
        card.setAttribute('role', 'alert');
        card.innerHTML = '<h3>' + escapeHTML(model.title || '任务遇到问题') + '</h3>' +
            '<p>' + escapeHTML(model.summary || '请检查服务配置后重试。') + '</p>';
        if (model.details) {
            var details = document.createElement('details');
            var summary = document.createElement('summary');
            summary.textContent = '查看技术详情';
            var pre = document.createElement('pre');
            pre.textContent = truncateText(model.details, 3000);
            details.appendChild(summary);
            details.appendChild(pre);
            card.appendChild(details);
        }
        return card;
    }

    async function submitComposer() {
        if (state.submitting) return;
        var message = dom.taskInput.value.trim();
        if (!message && !state.attachments.length) return;
        try {
            var attachments = await readyAttachments();
            if (state.currentTask && ACTIVE_STATUSES[String(state.currentTask.status || '').toLowerCase()] && !attachments.length) {
                var injected = await injectFollowup(message);
                if (injected) return;
            }
            await submitNewTask(message, attachments);
        } catch (error) {
            showRequestError(error, '任务没有发送成功');
        }
    }

    async function injectFollowup(message) {
        setSubmitting(true);
        try {
            var data = await getJSON('/api/interrupt', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json; charset=utf-8' },
                body: JSON.stringify({
                    session_id: state.currentSessionId,
                    task_id: state.currentTask.id,
                    message: message
                })
            });
            if (data.status !== 'injected') {
                state.currentTask = null;
                return false;
            }
            state.messages.push({
                role: 'user',
                content: message,
                created_at: new Date().toISOString()
            });
            state.followupAck = '补充要求已加入当前任务';
            clearComposer();
            renderTimeline();
            updateComposerState();
            scrollTimelineToBottom(true);
            window.setTimeout(function () {
                state.followupAck = '';
                renderTimeline();
            }, 5000);
            return true;
        } finally {
            setSubmitting(false);
        }
    }

    async function submitNewTask(message, attachments) {
        setSubmitting(true);
        state.taskError = null;
        try {
            if (!state.currentSessionId) await createSession();
            setView('conversation');
            var userMessage = {
                role: 'user',
                content: message,
                attachments: attachments,
                created_at: new Date().toISOString()
            };
            state.messages.push(userMessage);
            renderSessionHeader();
            renderTimeline();
            scrollTimelineToBottom(true);

            var data = await getJSON('/api/chat', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json; charset=utf-8' },
                body: JSON.stringify({
                    task_type: 'chat',
                    agent_name: state.selectedAgent || '',
                    message: message,
                    attachments: attachments,
                    session_id: state.currentSessionId,
                    channel: 'web',
                    user_id: userID()
                })
            });
            clearComposer();

            if (data.task_id) {
                state.currentTask = {
                    id: data.task_id,
                    status: data.status || 'running',
                    session_id: state.currentSessionId,
                    message: message,
                    agent_name: state.selectedAgent || '',
                    created_at: data.timestamp || new Date().toISOString()
                };
                state.progressEvents = [{
                    title: data.status === 'pending' ? '任务正在排队' : '已理解目标',
                    detail: data.status === 'pending' ? '等待可用执行槽位。' : '正在分析任务并准备执行。',
                    state: 'running',
                    timestamp: data.timestamp || new Date().toISOString()
                }];
                updateTaskState(state.currentTask.status);
                updateComposerState();
                renderTimeline();
                renderDetails();
                subscribeCurrentTask(state.currentTask);
            } else if (data.result !== undefined) {
                state.messages.push({
                    role: 'assistant',
                    content: resultText(data.result),
                    agent_name: state.selectedAgent || 'MACS',
                    created_at: data.timestamp || new Date().toISOString()
                });
                state.currentTask = null;
                updateTaskState('completed');
                renderTimeline();
            } else if (data.type === 'error' || data.error || data.message) {
                throw new RequestError(data.message || data.error || '模型没有返回可用结果', 502, JSON.stringify(data, null, 2));
            }

            refreshSessions(false);
            refreshBackgroundState();
        } catch (error) {
            state.taskError = requestErrorModel(error, '任务没有发送成功');
            renderTimeline();
            updateTaskState('failed');
            throw error;
        } finally {
            setSubmitting(false);
        }
    }

    async function createSession() {
        var data = await getJSON('/api/sessions', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json; charset=utf-8' },
            body: JSON.stringify({ agent_name: state.selectedAgent || '', user_id: userID() })
        });
        if (!data.session || !data.session.id) {
            throw new RequestError('服务没有返回会话 ID', 500, JSON.stringify(data, null, 2));
        }
        state.currentSessionId = data.session.id;
        state.project = data.project || state.project;
        state.sessions.unshift(data.session);
        storageSet(STORAGE.session, state.currentSessionId);
        updateSessionURL(state.currentSessionId);
        renderProject();
        renderSidebar();
    }

    async function stopCurrentTask() {
        if (!state.currentTask || !state.currentTask.id) return;
        var taskID = state.currentTask.id;
        dom.stopTaskButton.disabled = true;
        try {
            await getJSON('/api/task/stop/' + encodeURIComponent(taskID), { method: 'POST' });
            state.currentTask.status = 'stopped';
            state.taskError = {
                title: '任务已停止',
                summary: '执行已按你的要求停止，已有消息和产物仍然保留。',
                details: ''
            };
            closeTaskSubscription();
            updateTaskState('stopped');
            updateComposerState();
            renderTimeline();
            showToast('任务已停止', 'success');
            refreshBackgroundState();
        } catch (error) {
            showRequestError(error, '无法停止当前任务');
        } finally {
            dom.stopTaskButton.disabled = false;
        }
    }

    function subscribeCurrentTask(task) {
        if (!task || !task.id || !ACTIVE_STATUSES[String(task.status || '').toLowerCase()]) return;
        closeTaskSubscription();
        if (!window.EventSource) {
            startTaskPolling(task.id);
            return;
        }
        var clientID = 'workbench_v2_' + Date.now();
        var source = new EventSource('/api/events?task_id=' + encodeURIComponent(task.id) +
            '&client_id=' + encodeURIComponent(clientID));
        state.eventSource = source;
        source.addEventListener('progress', function (event) {
            var data = safeJSON(event.data);
            if (!data) return;
            if (data.type === 'heartbeat') return;
            appendProgress(data);
        });
        source.addEventListener('task_update', function (event) {
            var data = safeJSON(event.data);
            if (!data || !TERMINAL_STATUSES[String(data.status || '').toLowerCase()]) return;
            finishTask(data);
        });
        source.onerror = function () {
            if (state.eventSource === source) {
                source.close();
                state.eventSource = null;
                startTaskPolling(task.id);
            }
        };
    }

    function startTaskPolling(taskID) {
        if (!taskID) return;
        window.clearTimeout(state.pollTimer);
        state.pollTimer = window.setTimeout(async function poll() {
            if (!state.currentTask || state.currentTask.id !== taskID) return;
            try {
                var data = await getJSON('/api/task/' + encodeURIComponent(taskID));
                if (TERMINAL_STATUSES[String(data.status || '').toLowerCase()]) {
                    finishTask(data);
                    return;
                }
                state.currentTask.status = data.status || state.currentTask.status;
                updateTaskState(state.currentTask.status);
            } catch (error) {
                if (error.status === 404) {
                    refreshBackgroundState();
                    return;
                }
            }
            state.pollTimer = window.setTimeout(poll, 2500);
        }, 1200);
    }

    async function finishTask(data) {
        var taskID = data.task_id || (state.currentTask && state.currentTask.id);
        if (!taskID || state.finishingTasks[taskID]) return;
        state.finishingTasks[taskID] = true;
        closeTaskSubscription();
        if (state.currentTask && state.currentTask.id === taskID) state.currentTask.status = data.status;
        appendProgress({
            type: data.status === 'completed' || data.status === 'degraded' ? 'complete' : 'error',
            status: data.status,
            message: data.status === 'degraded' ? '任务已完成，部分能力受限' :
                data.status === 'completed' ? '任务已完成' :
                    data.status === 'stopped' ? '任务已停止' : '任务执行失败',
            timestamp: data.timestamp || new Date().toISOString()
        });
        if (data.status === 'failed') {
            state.taskError = requestErrorModel(
                new RequestError(data.error || '任务执行失败', 500, data.error || ''),
                '任务执行失败'
            );
        } else if (data.status === 'stopped') {
            state.taskError = {
                title: '任务已停止',
                summary: '执行已经停止，已有记录和产物仍然保留。',
                details: ''
            };
        }
        updateTaskState(data.status);
        updateComposerState();
        renderTimeline();
        await delay(220);
        await Promise.allSettled([refreshSessions(false), refreshArtifacts(false), refreshBackgroundState()]);
        if (state.currentSessionId) {
            await loadSession(state.currentSessionId, {
                updateURL: false,
                closeSidebar: false,
                preserveError: data.status === 'failed' || data.status === 'stopped'
            });
        } else if (data.result !== undefined && (data.status === 'completed' || data.status === 'degraded')) {
            state.messages.push({
                role: 'assistant',
                content: resultText(data.result),
                created_at: new Date().toISOString(),
                agent_name: 'MACS'
            });
            renderTimeline();
        }
        delete state.finishingTasks[taskID];
    }

    function appendProgress(data) {
        var model = progressModel(data);
        if (!model) return;
        var last = state.progressEvents[state.progressEvents.length - 1];
        if (last && last.key === model.key) {
            state.progressEvents[state.progressEvents.length - 1] = model;
        } else {
            state.progressEvents.push(model);
        }
        if (state.progressEvents.length > 30) state.progressEvents = state.progressEvents.slice(-30);
        renderTimeline();
        scrollTimelineToBottom(true);
    }

    function progressModel(data) {
        data = data || {};
        var type = String(data.type || 'progress').toLowerCase();
        var status = String(data.status || '').toLowerCase();
        var tool = String(data.tool_name || data.tool || '').trim();
        var title = '';
        var detail = '';
        var rowState = status === 'failed' || status === 'error' || type === 'error' ? 'failed' :
            status === 'completed' || status === 'success' || type === 'tool_end' || type === 'complete' ? 'completed' :
                'running';

        if (type === 'task_start') {
            title = cleanProgressText(data.message) || '已理解目标';
        } else if (type === 'thinking' || type === 'llm_call') {
            title = '正在分析和整理';
            detail = 'MACS 正在理解上下文并确定下一步。';
        } else if (type === 'compress') {
            title = '正在整理上下文';
        } else if (type === 'tool_start' || type === 'tool_end' || tool) {
            title = friendlyToolName(tool);
            if (type === 'tool_end') title += rowState === 'failed' ? '未完成' : '已完成';
        } else if (type === 'complete') {
            title = cleanProgressText(data.message) || '任务已完成';
        } else if (type.indexOf('stage_') === 0) {
            title = cleanProgressText(data.step_name || data.message) || '正在执行任务步骤';
        } else if (type === 'error') {
            title = '当前步骤遇到问题';
            detail = 'MACS 正在尝试恢复；技术细节会保留在任务日志中。';
        } else {
            title = cleanProgressText(data.message) || '正在处理';
        }
        return {
            key: [type, tool, data.tool_call_id || '', title].join('|'),
            title: truncateText(title, 80),
            detail: truncateText(detail, 150),
            state: rowState,
            timestamp: data.timestamp || data.ts || new Date().toISOString()
        };
    }

    function friendlyToolName(tool) {
        var key = String(tool || '').toLowerCase().replace(/^functions[.:/_-]+/, '').replace(/^tools[.:/_-]+/, '');
        if (/shell|bash|powershell|terminal|exec|command/.test(key)) return '运行命令';
        if (/read|file|list|glob/.test(key) && !/write|edit/.test(key)) return '读取和整理文件';
        if (/grep|search/.test(key)) return '搜索相关信息';
        if (/write|edit|patch/.test(key)) return '更新文件';
        if (/browser|web_fetch|navigate|open_url/.test(key)) return '访问网页';
        if (/skill/.test(key)) return '调用专业能力';
        if (/image/.test(key)) return '处理图片';
        if (/plan/.test(key)) return '更新任务计划';
        if (/git/.test(key)) return '检查代码变更';
        return tool ? '执行 ' + truncateText(tool.replace(/[_-]+/g, ' '), 32) : '执行任务步骤';
    }

    function syncCurrentTask() {
        var active = activeTaskForSession(state.currentSessionId);
        if (active && (!state.currentTask || state.currentTask.id !== active.id)) {
            state.currentTask = active;
            state.progressEvents = [];
            updateTaskState(active.status);
            updateComposerState();
            if (dom.workbenchShell.getAttribute('data-view') === 'conversation') {
                renderTimeline();
                renderDetails();
                subscribeCurrentTask(active);
            }
        } else if (!active && state.currentTask &&
            ACTIVE_STATUSES[String(state.currentTask.status || '').toLowerCase()]) {
            startTaskPolling(state.currentTask.id);
        }
        renderDetails();
    }

    function closeTaskSubscription() {
        if (state.eventSource) {
            state.eventSource.close();
            state.eventSource = null;
        }
        window.clearTimeout(state.pollTimer);
        state.pollTimer = null;
    }

    function updateTaskState(status) {
        status = String(status || 'idle').toLowerCase();
        dom.taskState.setAttribute('data-status', status);
        var label = dom.taskState.querySelector('span:last-child');
        if (label) label.textContent = statusLabel(status);
    }

    function updateComposerState() {
        var active = state.currentTask && ACTIVE_STATUSES[String(state.currentTask.status || '').toLowerCase()];
        dom.stopTaskButton.hidden = !active;
        dom.taskInput.placeholder = active ? '继续提出要求…' : '描述任务，或拖入文件';
        dom.composerHint.textContent = active ? '发送后会加入当前任务' : 'Enter 发送 · Shift+Enter 换行';
    }

    function setSubmitting(isSubmitting) {
        state.submitting = !!isSubmitting;
        dom.sendButton.disabled = state.submitting;
        dom.sendButton.classList.toggle('is-busy', state.submitting);
        dom.sendButton.innerHTML = state.submitting ?
            '<i data-lucide="loader-circle" aria-hidden="true"></i>' :
            '<i data-lucide="arrow-up" aria-hidden="true"></i>';
        refreshIcons();
    }

    function clearComposer() {
        dom.taskInput.value = '';
        state.attachments = [];
        renderAttachments();
        resizeInput();
    }

    function resizeInput() {
        dom.taskInput.style.height = 'auto';
        dom.taskInput.style.height = Math.min(dom.taskInput.scrollHeight, 220) + 'px';
    }

    function queueFiles(files) {
        if (!files.length) return;
        var remaining = Math.max(0, 20 - state.attachments.length);
        if (files.length > remaining) showToast('每条消息最多添加 20 个附件', 'error');
        files.slice(0, remaining).forEach(function (file) {
            if (file.size > 50 * 1024 * 1024) {
                showToast('“' + file.name + '”超过 50MB，未添加', 'error');
                return;
            }
            var item = {
                id: 'file_' + Date.now() + '_' + Math.random().toString(36).slice(2, 8),
                name: file.name,
                size: file.size,
                mime_type: file.type || '',
                kind: String(file.type || '').indexOf('image/') === 0 ? 'image' : 'file',
                status: 'uploading',
                path: '',
                error: ''
            };
            state.attachments.push(item);
            item.promise = uploadAttachment(item, file);
        });
        renderAttachments();
    }

    async function uploadAttachment(item, file) {
        var form = new FormData();
        form.append('file', file, file.name);
        try {
            var data = await getJSON('/api/upload', { method: 'POST', body: form });
            item.name = data.filename || item.name;
            item.path = data.path || '';
            item.size = data.size || item.size;
            item.mime_type = data.type || item.mime_type;
            item.kind = data.kind || item.kind;
            item.status = 'ready';
            if (!item.path) throw new Error('上传结果缺少文件路径');
            return item;
        } catch (error) {
            item.status = 'error';
            item.error = error.message || '上传失败';
            throw error;
        } finally {
            renderAttachments();
        }
    }

    async function readyAttachments() {
        var snapshot = state.attachments.slice();
        var results = await Promise.allSettled(snapshot.map(function (item) {
            return item.promise || Promise.resolve(item);
        }));
        var failedIndex = results.findIndex(function (result) { return result.status === 'rejected'; });
        if (failedIndex >= 0) {
            throw new RequestError('附件“' + snapshot[failedIndex].name + '”上传失败', 400,
                snapshot[failedIndex].error || '');
        }
        return snapshot.map(function (item) {
            return {
                name: item.name,
                path: item.path,
                mime_type: item.mime_type,
                size: item.size,
                kind: item.kind
            };
        });
    }

    function renderAttachments() {
        dom.attachmentStrip.hidden = state.attachments.length === 0;
        dom.attachmentStrip.innerHTML = state.attachments.map(function (item) {
            return '<div class="attachment-row is-' + escapeAttribute(item.status) + '" title="' +
                escapeAttribute(item.error || item.name) + '"><i data-lucide="' +
                (item.kind === 'image' ? 'image' : 'file') + '" aria-hidden="true"></i><span>' +
                escapeHTML(item.name) + '</span><button type="button" data-remove-attachment="' +
                escapeAttribute(item.id) + '" aria-label="移除 ' + escapeAttribute(item.name) + '">' +
                '<i data-lucide="x" aria-hidden="true"></i></button></div>';
        }).join('');
        refreshIcons();
    }

    function removeAttachment(id) {
        state.attachments = state.attachments.filter(function (item) { return item.id !== id; });
        renderAttachments();
    }

    function renderHealth() {
        var healthy = state.health && state.health.status === 'healthy' && state.health.db !== false;
        dom.serviceStatus.setAttribute('data-status', healthy ? 'healthy' : (state.health ? 'error' : 'checking'));
        dom.serviceStatus.setAttribute('aria-label', '服务状态：' + (healthy ? '正常' : '异常'));
        var label = dom.serviceStatus.querySelector('.service-label');
        if (label) label.textContent = healthy ? '服务正常' : (state.health ? '服务异常' : '检查服务');
    }

    function describeServiceStatus() {
        if (!state.health) {
            showToast('正在检查 MACS 服务状态');
            return;
        }
        if (state.health.status === 'healthy' && state.health.db !== false) {
            showToast('服务正常 · ' + (state.health.running_tasks || 0) + ' 个任务正在运行', 'success');
        } else {
            showToast('服务或数据库状态异常，请打开系统状态查看详情', 'error');
        }
    }

    function renderProject() {
        var project = state.project || {};
        var rawName = String(project.name || '').trim();
        var displayName = !rawName || /^(default|openkitty|\.)$/i.test(rawName) ? '默认工作区' : rawName;
        dom.workspaceName.textContent = displayName;
        dom.topbarWorkspaceName.textContent = displayName;
        var running = state.tasks.filter(function (task) {
            return ACTIVE_STATUSES[String(task.status || '').toLowerCase()];
        }).length;
        var rows = [
            ['项目', displayName],
            ['工作目录', project.workspace || '当前启动目录'],
            ['最近任务', String(state.sessions.length)],
            ['运行中', String(running)],
            ['产物', String(state.artifacts.length)]
        ];
        dom.projectSummary.innerHTML = rows.map(function (row) {
            return '<div><dt>' + escapeHTML(row[0]) + '</dt><dd>' + escapeHTML(row[1]) + '</dd></div>';
        }).join('');
    }

    function renderAgents() {
        var preferred = state.selectedAgent;
        var agents = state.agents.slice().sort(function (a, b) {
            function rank(item) {
                var name = String(item.name || '').toLowerCase();
                if (preferred && item.name === preferred) return -1;
                if (name === 'assistant') return 0;
                if (name.indexOf('assist') === 0) return 1;
                return 2;
            }
            return rank(a) - rank(b) || String(a.name || '').localeCompare(String(b.name || ''));
        });
        dom.agentSelect.innerHTML = '<option value="">自动选择</option>' + agents.map(function (agent) {
            return '<option value="' + escapeAttribute(agent.name) + '"' +
                (agent.name === state.selectedAgent ? ' selected' : '') + '>' +
                escapeHTML(agent.name) + '</option>';
        }).join('');
        updateAgentPresentation();
    }

    function selectAgent() {
        state.selectedAgent = dom.agentSelect.value || '';
        storageSet(STORAGE.agent, state.selectedAgent);
        updateAgentPresentation();
    }

    function updateAgentPresentation() {
        var agent = state.agents.find(function (item) { return item.name === state.selectedAgent; });
        dom.modelLabel.textContent = agent ? truncateText(agent.name, 16) : 'Assistant';
        if (!agent) {
            dom.agentHelp.textContent = 'MACS 会使用当前可用的默认 Agent。';
            return;
        }
        var model = agent.model || agent.model_config || '';
        dom.agentHelp.textContent = model ? '当前模型：' + model : '当前选择：' + agent.name;
    }

    function renderCapabilities() {
        dom.employeeCount.textContent = String(state.employees.length);
        dom.skillCount.textContent = String(state.skills.length);
        dom.employeeList.innerHTML = capabilityHTML(
            state.employees.slice(0, 8),
            function (item) { return '@' + (item.at_name || item.name || ''); },
            function (item) { return item.name || item.at_name || 'AI 员工'; },
            function (item) { return item.role || item.note || item.status || '业务执行'; },
            'briefcase-business'
        );
        dom.skillList.innerHTML = capabilityHTML(
            state.skills.slice(0, 8),
            function (item) { return '@' + (item.name || ''); },
            function (item) { return item.display_name || item.name || 'Skill'; },
            function (item) { return item.short_description || item.description || '专业能力'; },
            'sparkles'
        );
        if (!state.employees.length) dom.employeeList.innerHTML = '<p class="field-help">当前没有可用的 AI 员工。</p>';
        if (!state.skills.length) dom.skillList.innerHTML = '<p class="field-help">当前没有可用的 Skill。</p>';
        refreshIcons();
    }

    function capabilityHTML(items, mention, title, description, icon) {
        return items.map(function (item) {
            return '<button class="capability-option" type="button" data-mention="' +
                escapeAttribute(mention(item)) + '"><span class="capability-option-icon"><i data-lucide="' +
                icon + '" aria-hidden="true"></i></span><span><strong>' +
                escapeHTML(title(item)) + '</strong><small>' +
                escapeHTML(truncateText(description(item), 40)) + '</small></span></button>';
        }).join('');
    }

    function openCapabilities() {
        openDialog(dom.capabilityDialog);
    }

    function chooseMention(mention) {
        var prefix = String(mention || '').trim();
        if (!prefix) return;
        var current = dom.taskInput.value.trim();
        dom.taskInput.value = prefix + (current ? ' ' + current : ' ');
        closeDialog(dom.capabilityDialog);
        resizeInput();
        dom.taskInput.focus();
        dom.taskInput.setSelectionRange(dom.taskInput.value.length, dom.taskInput.value.length);
    }

    function openInspector(tab) {
        selectInspectorTab(tab || 'artifacts');
        dom.workbenchShell.classList.add('is-inspector-open');
        dom.inspector.setAttribute('aria-hidden', 'false');
        renderArtifacts();
        renderDetails();
    }

    function closeInspector() {
        dom.workbenchShell.classList.remove('is-inspector-open');
        dom.inspector.setAttribute('aria-hidden', 'true');
    }

    function selectInspectorTab(tab) {
        var artifacts = tab !== 'details';
        dom.artifactsTab.setAttribute('aria-selected', String(artifacts));
        dom.detailsTab.setAttribute('aria-selected', String(!artifacts));
        dom.artifactsPanel.hidden = !artifacts;
        dom.detailsPanel.hidden = artifacts;
    }

    function renderArtifacts() {
        if (!state.artifacts.length) {
            dom.artifactList.innerHTML = '<div class="artifact-empty-state"><i data-lucide="package-open" aria-hidden="true"></i>' +
                '完成带文件输出的任务后，最终产物会集中出现在这里。</div>';
            refreshIcons();
            return;
        }
        dom.artifactList.innerHTML = state.artifacts.slice(0, 30).map(function (artifact) {
            var type = artifactType(artifact);
            var href = safeURL(artifact.download_url || '');
            var action = href ? '<a class="artifact-action" href="' + escapeAttribute(href) +
                '" target="_blank" rel="noopener" aria-label="下载 ' + escapeAttribute(artifact.name || '产物') +
                '"><i data-lucide="download" aria-hidden="true"></i></a>' :
                '<span class="artifact-action" aria-hidden="true"><i data-lucide="ellipsis" aria-hidden="true"></i></span>';
            return '<article class="artifact-row-v2"><span class="artifact-type-icon" data-type="' +
                escapeAttribute(type) + '"><i data-lucide="' + artifactIcon(type) + '" aria-hidden="true"></i></span>' +
                '<div class="artifact-copy"><span class="artifact-name">' +
                escapeHTML(artifact.name || artifact.path || '未命名产物') + '</span><span class="artifact-meta"><span>' +
                escapeHTML(type.toUpperCase()) + '</span><span>' +
                escapeHTML(statusLabel(artifact.status || 'completed')) + '</span><span>' +
                escapeHTML(relativeTime(artifact.created_at || artifact.mod_time)) + '</span></span></div>' +
                action + '</article>';
        }).join('');
        refreshIcons();
    }

    function renderDetails() {
        var session = sessionByID(state.currentSessionId);
        var task = state.currentTask;
        var rows = [
            ['状态', statusLabel(task ? task.status : (state.currentSessionId ? 'completed' : 'idle'))],
            ['任务', task && task.id ? task.id : '—'],
            ['会话', state.currentSessionId || '尚未创建'],
            ['Agent', task && task.agent_name ? task.agent_name : (state.selectedAgent || '自动选择')],
            ['项目', dom.workspaceName ? dom.workspaceName.textContent : '默认工作区'],
            ['更新时间', relativeTime((task && task.created_at) || (session && session.updated_at))]
        ];
        dom.detailList.innerHTML = rows.map(function (row) {
            return '<div><dt>' + escapeHTML(row[0]) + '</dt><dd>' + escapeHTML(row[1]) + '</dd></div>';
        }).join('');
        if (task && task.id) {
            dom.progressLogLink.hidden = false;
            dom.progressLogLink.href = '/api/task/progress-log/' + encodeURIComponent(task.id);
            dom.progressLogLink.setAttribute('download', 'progress_' + task.id + '.log');
        } else {
            dom.progressLogLink.hidden = true;
            dom.progressLogLink.removeAttribute('href');
        }
        refreshIcons();
    }

    function openSearch() {
        renderSearchResults();
        openDialog(dom.searchDialog);
        window.setTimeout(function () {
            dom.searchInput.focus();
            dom.searchInput.select();
        }, 20);
    }

    function renderSearchResults() {
        var query = String(dom.searchInput.value || '').trim().toLowerCase();
        var sessions = state.sessions.filter(function (session) {
            if (!query) return true;
            return [sessionTitle(session), session.id, session.first_question]
                .join(' ').toLowerCase().indexOf(query) >= 0;
        }).slice(0, 20);
        if (!sessions.length) {
            dom.searchResults.innerHTML = '<div class="session-empty">没有找到相关任务。</div>';
            return;
        }
        dom.searchResults.innerHTML = sessions.map(function (session) {
            return '<button class="search-result" type="button" data-session-id="' +
                escapeAttribute(session.id) + '"><span class="search-result-icon"><i data-lucide="file-text" aria-hidden="true"></i></span>' +
                '<span><strong>' + escapeHTML(sessionTitle(session)) + '</strong><small>' +
                escapeHTML(truncateText(session.first_question || '任务记录', 80)) + '</small></span><time>' +
                escapeHTML(shortAge(session.updated_at)) + '</time></button>';
        }).join('');
        refreshIcons();
    }

    function handleNavigation(target) {
        if (target === 'project') {
            openDialog(dom.projectDialog);
        } else if (target === 'running') {
            var active = state.tasks.find(function (task) {
                return ACTIVE_STATUSES[String(task.status || '').toLowerCase()];
            });
            if (active && active.session_id) loadSession(active.session_id);
            else showToast('当前没有运行中的任务');
        } else {
            closeSidebar();
            if (state.currentSessionId) loadSession(state.currentSessionId);
            else startNewTask();
        }
    }

    function handleMobileNavigation(target) {
        document.querySelectorAll('[data-mobile-nav]').forEach(function (button) {
            var selected = button.getAttribute('data-mobile-nav') === target;
            button.classList.toggle('is-active', selected);
            if (selected) button.setAttribute('aria-current', 'page');
            else button.removeAttribute('aria-current');
        });
        if (target === 'tasks') {
            if (state.currentSessionId) setView('conversation');
            else setView('welcome');
        } else if (target === 'project') {
            openDialog(dom.projectDialog);
        } else if (target === 'capability') {
            openCapabilities();
        }
    }

    function toggleMoreMenu() {
        var open = dom.moreMenu.hidden;
        dom.moreMenu.hidden = !open;
        dom.moreButton.setAttribute('aria-expanded', String(open));
        if (open) {
            var first = dom.moreMenu.querySelector('[role="menuitem"]');
            if (first) first.focus();
        }
    }

    function closeMoreMenu() {
        dom.moreMenu.hidden = true;
        dom.moreButton.setAttribute('aria-expanded', 'false');
    }

    function openSidebar() {
        if (window.matchMedia('(min-width: 901px)').matches) {
            dom.workbenchShell.classList.remove('is-sidebar-collapsed');
            return;
        }
        dom.workbenchShell.classList.add('is-sidebar-open');
        dom.mobileScrim.hidden = false;
        dom.mobileMenuButton.setAttribute('aria-expanded', 'true');
    }

    function closeSidebar() {
        dom.workbenchShell.classList.remove('is-sidebar-open');
        dom.mobileScrim.hidden = true;
        dom.mobileMenuButton.setAttribute('aria-expanded', 'false');
    }

    function collapseSidebar() {
        if (window.matchMedia('(min-width: 901px)').matches) {
            dom.workbenchShell.classList.add('is-sidebar-collapsed');
            dom.mobileMenuButton.setAttribute('aria-expanded', 'false');
            return;
        }
        closeSidebar();
    }

    function openDialog(dialog) {
        if (!dialog) return;
        closeMoreMenu();
        if (typeof dialog.showModal === 'function') {
            if (!dialog.open) dialog.showModal();
        } else {
            dialog.setAttribute('open', '');
        }
    }

    function closeDialog(dialog) {
        if (!dialog || !dialog.open) return;
        if (typeof dialog.close === 'function') dialog.close();
        else dialog.removeAttribute('open');
    }

    function renderMarkdown(text) {
        var source = escapeHTML(String(text || ''));
        var html;
        try {
            html = window.marked && typeof window.marked.parse === 'function' ?
                window.marked.parse(source, { gfm: true, breaks: true }) :
                '<p>' + source.replace(/\n/g, '<br>') + '</p>';
        } catch (error) {
            html = '<p>' + source.replace(/\n/g, '<br>') + '</p>';
        }
        var template = document.createElement('template');
        template.innerHTML = html;
        template.content.querySelectorAll('a').forEach(function (link) {
            var href = safeURL(link.getAttribute('href') || '');
            if (!href) {
                link.removeAttribute('href');
                return;
            }
            link.setAttribute('href', href);
            if (/^https?:/i.test(href)) {
                link.target = '_blank';
                link.rel = 'noopener noreferrer';
            }
        });
        template.content.querySelectorAll('img').forEach(function (image) {
            var src = safeURL(image.getAttribute('src') || '');
            if (!src) {
                image.remove();
                return;
            }
            image.setAttribute('src', src);
            image.setAttribute('loading', 'lazy');
            image.setAttribute('referrerpolicy', 'no-referrer');
        });
        return template.innerHTML;
    }

    function safeURL(value) {
        var url = String(value || '').trim();
        if (!url) return '';
        if (/^(https?:|mailto:|#|\/(?!\/)|\.{0,2}\/)/i.test(url)) return url;
        return '';
    }

    function requestErrorModel(error, fallback) {
        var details = error && (error.details || error.message) ? String(error.details || error.message) : '';
        var status = error && error.status;
        var summary = fallback || '请求没有完成';
        if (status === 401 || status === 403) summary = '当前账号没有访问这项能力，请检查登录状态。';
        else if (status === 404) summary = '请求的任务不存在，可能已经被清理或属于其他工作区。';
        else if (status === 409) summary = '当前任务状态已变化，请刷新后重试。';
        else if (status === 429) summary = '请求过于频繁，请稍后再试。';
        else if (status >= 500 || /provider|model|api error|upstream/i.test(details)) {
            summary = '模型或执行服务暂时不可用，请检查配置后重试。';
        } else if (error && error.message) {
            summary = cleanErrorSummary(error.message, summary);
        }
        return {
            title: fallback || '任务遇到问题',
            summary: summary,
            details: details
        };
    }

    function cleanErrorSummary(value, fallback) {
        var text = String(value || '').trim();
        var parsed = safeJSON(text);
        if (parsed) text = parsed.error || parsed.message || fallback;
        text = text.replace(/\s+/g, ' ').replace(/^HTTP\s+\d+\s*[:\-]?\s*/i, '');
        if (!text || text.length > 220 || /^[{\[]/.test(text)) return fallback;
        return text;
    }

    function showRequestError(error, fallback) {
        var model = requestErrorModel(error, fallback);
        showToast(model.summary, 'error');
    }

    function showToast(message, type) {
        var toast = document.createElement('div');
        toast.className = 'toast' + (type ? ' is-' + type : '');
        toast.innerHTML = '<i data-lucide="' + (type === 'error' ? 'circle-alert' : type === 'success' ? 'circle-check' : 'info') +
            '" aria-hidden="true"></i><span>' + escapeHTML(message) + '</span>';
        dom.toastRegion.appendChild(toast);
        refreshIcons();
        window.setTimeout(function () {
            toast.remove();
        }, 4200);
    }

    async function getJSON(url, options) {
        var response;
        try {
            response = await fetch(url, options || {});
        } catch (error) {
            throw new RequestError('无法连接 MACS 服务', 0, error.message || '');
        }
        var text = await response.text();
        var data = safeJSON(text);
        if (!response.ok) {
            var message = data && (data.error || data.message) ? data.error || data.message :
                'HTTP ' + response.status;
            throw new RequestError(message, response.status, text);
        }
        return data === null ? {} : data;
    }

    function RequestError(message, status, details) {
        this.name = 'RequestError';
        this.message = message || '请求失败';
        this.status = status || 0;
        this.details = details || '';
    }
    RequestError.prototype = Object.create(Error.prototype);
    RequestError.prototype.constructor = RequestError;

    function activeTaskForSession(sessionID) {
        if (!sessionID) return null;
        return state.tasks.find(function (task) {
            return task.session_id === sessionID &&
                ACTIVE_STATUSES[String(task.status || '').toLowerCase()];
        }) || null;
    }

    function taskForSession(sessionID) {
        var active = activeTaskForSession(sessionID);
        if (active) return active;
        return state.tasks.filter(function (task) {
            return task.session_id === sessionID;
        }).sort(function (a, b) {
            return taskSortTime(b) - taskSortTime(a);
        })[0] || null;
    }

    function taskSortTime(task) {
        var value = task && (task.completed_at || task.updated_at || task.created_at);
        var date = parseDateValue(value);
        return isNaN(date.getTime()) ? 0 : date.getTime();
    }

    function sessionByID(sessionID) {
        return state.sessions.find(function (session) { return session.id === sessionID; }) || null;
    }

    function sessionTitle(session) {
        if (!session) return '新任务';
        var title = String(session.first_question || '').replace(/\s+/g, ' ').trim();
        if (!title || title === session.id) {
            title = String(session.name || '').trim();
            if (!title || title === session.id) title = '新任务';
        }
        return truncateText(title, 30);
    }

    function sessionIcon(title) {
        title = String(title || '').toLowerCase();
        if (/代码|修复|开发|code|bug/.test(title)) return 'code-xml';
        if (/调研|分析|研究|search/.test(title)) return 'file-search';
        return 'file-text';
    }

    function firstUserText() {
        var message = state.messages.find(function (item) { return item.role === 'user' && item.content; });
        return message ? String(message.content).replace(/\s+/g, ' ').trim() : '';
    }

    function statusLabel(status) {
        var labels = {
            idle: '已就绪',
            pending: '排队中',
            running: '运行中',
            waiting: '等待中',
            waiting_approval: '等待确认',
            completed: '已完成',
            degraded: '降级完成',
            failed: '失败',
            stopped: '已停止',
            success: '已完成'
        };
        return labels[String(status || '').toLowerCase()] || String(status || '未知');
    }

    function artifactType(artifact) {
        var value = String((artifact && (artifact.type || artifact.name || artifact.path)) || 'file').toLowerCase();
        var match = value.match(/\.([a-z0-9]{1,8})(?:$|[?#])/);
        if (match) return match[1];
        if (/word|docx|doc/.test(value)) return 'docx';
        if (/excel|xlsx|xls|csv/.test(value)) return /csv/.test(value) ? 'csv' : 'xlsx';
        if (/pdf/.test(value)) return 'pdf';
        if (/image|png|jpg|jpeg|webp/.test(value)) return 'image';
        if (/html/.test(value)) return 'html';
        if (/code|source/.test(value)) return 'code';
        return value.length < 12 ? value : 'file';
    }

    function artifactIcon(type) {
        if (type === 'xlsx' || type === 'xls' || type === 'csv') return 'sheet';
        if (type === 'image' || /png|jpg|jpeg|webp/.test(type)) return 'image';
        if (type === 'code' || /html|js|go|py|json/.test(type)) return 'file-code-2';
        if (type === 'pdf') return 'file-type-2';
        return 'file-text';
    }

    function parseDateValue(value) {
        if (typeof value === 'number' && value > 0 && value < 1000000000000) {
            return new Date(value * 1000);
        }
        return value ? new Date(value) : new Date();
    }

    function formatClock(value) {
        var date = parseDateValue(value);
        if (isNaN(date.getTime())) date = new Date();
        return two(date.getHours()) + ':' + two(date.getMinutes());
    }

    function relativeTime(value) {
        if (!value) return '刚刚';
        var date = parseDateValue(value);
        if (isNaN(date.getTime())) return '刚刚';
        var seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
        if (seconds < 60) return '刚刚';
        if (seconds < 3600) return Math.floor(seconds / 60) + ' 分钟前';
        if (seconds < 86400) return Math.floor(seconds / 3600) + ' 小时前';
        if (seconds < 86400 * 7) return Math.floor(seconds / 86400) + ' 天前';
        return date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' });
    }

    function shortAge(value) {
        if (!value) return '';
        var date = parseDateValue(value);
        if (isNaN(date.getTime())) return '';
        var seconds = Math.max(0, Math.floor((Date.now() - date.getTime()) / 1000));
        if (seconds < 3600) return Math.max(1, Math.floor(seconds / 60)) + 'm';
        if (seconds < 86400) return Math.floor(seconds / 3600) + 'h';
        if (seconds < 86400 * 7) return Math.floor(seconds / 86400) + 'd';
        return (date.getMonth() + 1) + '/' + date.getDate();
    }

    function cleanProgressText(value) {
        var text = String(value || '')
            .replace(/\.openkitty[\\/][^\s]+/gi, '[内部路径]')
            .replace(/[A-Za-z]:[\\/][^\s"'<>)]*/g, '[内部路径]')
            .replace(/\s+/g, ' ')
            .trim();
        if (/^[{\[]/.test(text) || text.length > 180) return '';
        return text;
    }

    function resultText(value) {
        if (typeof value === 'string') return value;
        if (value === null || value === undefined) return '';
        try {
            return '~~~json\n' + JSON.stringify(value, null, 2) + '\n~~~';
        } catch (error) {
            return String(value);
        }
    }

    function sessionFromURL() {
        try {
            var params = new URLSearchParams(window.location.search);
            return params.get('session') || params.get('session_id') || '';
        } catch (error) {
            return '';
        }
    }

    function updateSessionURL(sessionID) {
        try {
            var url = new URL(window.location.href);
            if (sessionID) url.searchParams.set('session', sessionID);
            else url.searchParams.delete('session');
            url.searchParams.delete('session_id');
            window.history.pushState({}, '', url.pathname + url.search + url.hash);
        } catch (error) {
            return;
        }
    }

    function userID() {
        var value = storageGet(STORAGE.user);
        if (!value) {
            value = 'web';
            storageSet(STORAGE.user, value);
        }
        return value;
    }

    function scrollTimelineToBottom(smooth) {
        if (!dom.conversationView || dom.conversationView.hidden) return;
        window.requestAnimationFrame(function () {
            dom.conversationView.scrollTo({
                top: dom.conversationView.scrollHeight,
                behavior: smooth && !reducedMotion() ? 'smooth' : 'auto'
            });
        });
    }

    function refreshIcons() {
        if (window.lucide && typeof window.lucide.createIcons === 'function') {
            window.lucide.createIcons({
                attrs: { 'stroke-width': 1.75, 'aria-hidden': 'true' }
            });
        }
    }

    function safeJSON(value) {
        if (value === null || typeof value === 'object') return value;
        try { return JSON.parse(String(value)); } catch (error) { return null; }
    }

    function escapeHTML(value) {
        return String(value == null ? '' : value)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#39;');
    }

    function escapeAttribute(value) {
        return escapeHTML(value);
    }

    function truncateText(value, limit) {
        var chars = Array.from(String(value || ''));
        return chars.length > limit ? chars.slice(0, limit).join('') + '…' : chars.join('');
    }

    function storageGet(key) {
        try { return window.localStorage.getItem(key) || ''; } catch (error) { return ''; }
    }

    function storageSet(key, value) {
        try { window.localStorage.setItem(key, String(value || '')); } catch (error) { return; }
    }

    function storageRemove(key) {
        try { window.localStorage.removeItem(key); } catch (error) { return; }
    }

    function two(value) {
        return value < 10 ? '0' + value : String(value);
    }

    function delay(ms) {
        return new Promise(function (resolve) { window.setTimeout(resolve, ms); });
    }

    function reducedMotion() {
        return window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    }
}());
