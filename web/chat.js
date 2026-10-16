function esc(s) {
    if (!s) return '';
    return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function syncHeaderCompactControl(isCollapsed) {
    var btn = document.getElementById('headerCollapseBtn');
    if (!btn) return;
    var key = isCollapsed ? 'topbarExpandActions' : 'topbarCollapseActions';
    var fallback = isCollapsed ? '展开顶栏操作' : '收起顶栏操作';
    var label = typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
    btn.classList.toggle('is-collapsed', isCollapsed);
    btn.setAttribute('aria-expanded', isCollapsed ? 'false' : 'true');
    btn.setAttribute('aria-label', label);
    btn.setAttribute('data-i18n-title', key);
    btn.title = label;
}

function toggleHeaderCompact() {
    var hb = document.getElementById('headerButtons');
    if (!hb) return;
    var isCollapsed = hb.classList.contains('is-collapsed') || hb.style.display === 'none';
    var nextCollapsed = !isCollapsed;
    hb.style.removeProperty('display');
    hb.classList.toggle('is-collapsed', nextCollapsed);
    syncHeaderCompactControl(nextCollapsed);
}

document.addEventListener('openkitty:languagechange', function() {
    var hb = document.getElementById('headerButtons');
    if (hb) syncHeaderCompactControl(hb.classList.contains('is-collapsed'));
});

function setSessionHeaderTitle(text) {
    var title = document.getElementById('sessionTitle');
    if (!title) return;
    var titleText = document.getElementById('sessionTitleText');
    if (!titleText) {
        titleText = document.createElement('span');
        titleText.id = 'sessionTitleText';
        var collapse = document.getElementById('headerCollapseBtn');
        if (collapse && collapse.parentNode === title) {
            title.insertBefore(titleText, collapse.nextSibling);
        } else {
            title.insertBefore(titleText, title.firstChild);
        }
    }
    titleText.textContent = text || 'MACS';
}

function initMessageInputResize() {
    var handle = document.getElementById('messageInputResizeHandle');
    var input = document.getElementById('messageInput');
    if (!handle || !input || handle.dataset.resizeReady === 'true') return;
    handle.dataset.resizeReady = 'true';

    var storageKey = 'openkitty.messageInputHeight';
    var minHeight = 42;
    var maxHeight = function() {
        return Math.max(minHeight, Math.min(420, Math.floor(window.innerHeight * 0.5)));
    };
    var applyHeight = function(height, persist) {
        var next = Math.max(minHeight, Math.min(maxHeight(), Math.round(height)));
        input.style.height = next + 'px';
        if (persist) {
            try { localStorage.setItem(storageKey, String(next)); } catch (_) {}
        }
    };

    try {
        var savedHeight = parseInt(localStorage.getItem(storageKey), 10);
        if (Number.isFinite(savedHeight)) applyHeight(savedHeight, false);
    } catch (_) {}

    handle.addEventListener('pointerdown', function(event) {
        if (event.button !== 0) return;
        event.preventDefault();
        var startY = event.clientY;
        var startHeight = input.getBoundingClientRect().height;
        handle.classList.add('is-dragging');
        document.body.classList.add('is-resizing-message-input');
        if (handle.setPointerCapture) handle.setPointerCapture(event.pointerId);

        var move = function(moveEvent) {
            applyHeight(startHeight + startY - moveEvent.clientY, false);
        };
        var finish = function() {
            handle.removeEventListener('pointermove', move);
            handle.removeEventListener('pointerup', finish);
            handle.removeEventListener('pointercancel', finish);
            handle.classList.remove('is-dragging');
            document.body.classList.remove('is-resizing-message-input');
            applyHeight(input.getBoundingClientRect().height, true);
        };
        handle.addEventListener('pointermove', move);
        handle.addEventListener('pointerup', finish);
        handle.addEventListener('pointercancel', finish);
    });

    handle.addEventListener('dblclick', function() {
        input.style.height = '';
        try { localStorage.removeItem(storageKey); } catch (_) {}
    });
    handle.addEventListener('keydown', function(event) {
        var current = input.getBoundingClientRect().height;
        if (event.key === 'ArrowUp') applyHeight(current + 20, true);
        else if (event.key === 'ArrowDown') applyHeight(current - 20, true);
        else if (event.key === 'Home') applyHeight(minHeight, true);
        else if (event.key === 'End') applyHeight(maxHeight(), true);
        else return;
        event.preventDefault();
    });
}

document.addEventListener('DOMContentLoaded', initMessageInputResize);

// Chat module - messaging, rendering, collaboration
	
    
    // ====================== @mention autocomplete ======================
    var aeList = [];
    var skillMentionList = [];
    var skillMentionListReady = false;
    var atActiveIdx = -1;
    var atStartPos = -1;

    async function loadAEList() {
        try {
            var r = await fetch('/api/ae/list');
            var d = await r.json();
            aeList = (d.employees || []).map(function(e){ return {name:e.name, at:e.at_name||e.id, role:e.role||'', pipe:e.pipeline||''}; });
        } catch(e) { aeList = []; }
    }
    loadAEList();

    async function loadSkillMentionList() {
        try {
            var r = await fetch('/api/skills', {cache: 'no-store'});
            var d = await r.json();
            skillMentionList = (d.skills || []).map(function(skill) {
                return {
                    name: skill.display_name || skill.name,
                    at: skill.name,
                    role: skill.short_description || skill.description || '使用这个 Skill 运行当前任务',
                    pipe: 'Skill',
                    kind: 'skill',
                    aliases: Array.isArray(skill.aliases) ? skill.aliases : []
                };
            });
        } catch(e) { skillMentionList = []; }
        finally { skillMentionListReady = true; }
    }
    var skillMentionListLoad = loadSkillMentionList();

    async function ensureSkillMentionListReady() {
        if (!skillMentionListReady) await skillMentionListLoad;
    }

    function escapeAtMentionRegExp(value) {
        return String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }

    window.messageHasInstalledSkillMention = function(message) {
        return skillMentionList.some(function(skill) {
            return [skill.at].concat(skill.aliases || []).some(function(alias) {
                alias = String(alias || '').replace(/^@/, '').trim();
                if (!alias) return false;
                return new RegExp('(?:^|[^A-Za-z0-9_.-])@' + escapeAtMentionRegExp(alias) + '(?=$|[^\\p{L}\\p{N}\\p{M}_-])', 'iu').test(message);
            });
        });
    };

    document.addEventListener('DOMContentLoaded', function(){
        var ta = document.getElementById('messageInput');
        var dd = document.getElementById('atDropdown');
        if (!ta || !dd) return;

        ta.addEventListener('input', function(){
            var v = ta.value, p = ta.selectionStart;
            // Find @ before cursor
            var atI = -1;
            for (var i = p-1; i >= 0; i--) {
                if (v[i] === '@') { atI = i; break; }
                if (v[i] === ' ' || v[i] === '\n') break;
            }
            if (atI < 0) { dd.style.display = 'none'; atActiveIdx = -1; atStartPos = -1; return; }
            // @ must be start-of-line or after space
            if (atI > 0 && v[atI-1] !== ' ' && v[atI-1] !== '\n') { dd.style.display = 'none'; return; }
            var q = v.substring(atI+1, p).toLowerCase();
            atStartPos = atI;
            var matches = aeList.map(function(e) {
                return Object.assign({kind:'employee', aliases:[]}, e);
            }).concat(skillMentionList).filter(function(e){
                return e.at.toLowerCase().indexOf(q)>=0 || e.name.toLowerCase().indexOf(q)>=0 ||
                    (e.aliases || []).some(function(alias){ return String(alias).toLowerCase().indexOf(q)>=0; });
            }).slice(0, 20);
            if (matches.length === 0) { dd.style.display = 'none'; return; }
            atActiveIdx = 0;
            dd.innerHTML = matches.map(function(e){
                return '<div class="at-item" data-at="'+esc(e.at)+'" data-kind="'+esc(e.kind)+'" onmousedown="selectAtMention(this)">'+
                    '<span class="at-name">'+esc(e.name)+'</span><span class="at-atname">@'+esc(e.at)+'</span><span class="at-role">'+esc(e.role)+'</span>'+
                    (e.pipe?'<span class="at-pipe '+(e.kind === 'skill' ? 'at-kind-skill' : '')+'">'+esc(e.pipe)+'</span>':'')+'</div>';
            }).join('');
            dd.style.display = 'block';
            updateAtActive(dd.querySelectorAll('.at-item'));
        });

        ta.addEventListener('keydown', function(e){
            if (dd.style.display === 'none') return;
            var items = dd.querySelectorAll('.at-item');
            if (e.key === 'ArrowDown') { e.preventDefault(); atActiveIdx = Math.min(atActiveIdx+1, items.length-1); updateAtActive(items); }
            else if (e.key === 'ArrowUp') { e.preventDefault(); atActiveIdx = Math.max(atActiveIdx-1, 0); updateAtActive(items); }
            else if (e.key === 'Enter') { if (selectActiveAtMention(e)) e.stopImmediatePropagation(); }
            else if (e.key === 'Escape') { dd.style.display = 'none'; atActiveIdx = -1; }
        }, true);

        document.addEventListener('click', function(e){ if (e.target !== ta && !dd.contains(e.target)) { dd.style.display = 'none'; atActiveIdx = -1; } });
    });

    function updateAtActive(items) {
        items.forEach(function(it,i){ if(i===atActiveIdx){it.classList.add('active');it.scrollIntoView({block:'nearest'});} else it.classList.remove('active'); });
    }

    function selectAtMention(el) {
        var ta = document.getElementById('messageInput');
        var dd = document.getElementById('atDropdown');
        if (!ta || atStartPos < 0) return;
        var an = el.getAttribute('data-at');
        var before = ta.value.substring(0, atStartPos);
        var after = ta.value.substring(ta.selectionStart);
        ta.value = before + '@' + an + ' ' + after;
        ta.focus();
        ta.selectionStart = ta.selectionEnd = before.length + an.length + 2;
        dd.style.display = 'none';
        atStartPos = -1;
    }

    function selectActiveAtMention(e) {
        var dd = document.getElementById('atDropdown');
        if (!dd || dd.style.display === 'none') return false;
        var items = dd.querySelectorAll('.at-item');
        if (!items.length) return false;
        if (atActiveIdx < 0 || atActiveIdx >= items.length) atActiveIdx = 0;
        if (e) e.preventDefault();
        items[atActiveIdx].click();
        return true;
    }

    function hasOpenAtMentionDropdown() {
        var dd = document.getElementById('atDropdown');
        return !!(dd && dd.style.display !== 'none' && dd.querySelector('.at-item'));
    }


function handleEnterKey(e) {
		if (e.isComposing || e.keyCode === 229) return;

		// 检查是否按下了 Enter 键

		if (e.key === 'Enter') {
			if (!e.shiftKey && hasOpenAtMentionDropdown() && selectActiveAtMention(e)) {
				return;
			}

			// 检查是否同时按下了 Shift 键

			if (!e.shiftKey) {

				// 阻止默认行为（换行）

				e.preventDefault();

				// 获取输入框内容

				var input = document.getElementById('messageInput');

				if (!input) return;

				var message = input.value.trim();

				// 发送消息（web 端始终允许发送）
				if (message) {
					console.log('Enter key: send message');
					sendMessage();
					console.log('Enter key: interrupt injected, not stopping task');

				}

			}

			// 如果按了 Shift+Enter，允许换行（不做任何处理）

		}

	}

	async function refreshSessions() {

		addLog('info', '刷新会话列表...');

		await loadSessions();

		addLog('info', '会话列表已刷新');

	}

    function preferredAgentName(agentList) {

        var preferredName = arguments.length > 1 ? arguments[1] : (window._defaultAgentName || '');

        if (typeof window.sortAgentsAssistantFirst === 'function') {
            var ordered = window.sortAgentsAssistantFirst(agentList || [], preferredName || '');
            return ordered.length ? String(ordered[0].name || '') : '';
        }

        var assistPrefixMatch = '';

        for (var i = 0; i < agentList.length; i++) {

            var name = String(agentList[i].name || '').trim();
            var normalizedName = name.toLowerCase();
            if (normalizedName === 'assist') return name;
            if (!assistPrefixMatch && normalizedName.indexOf('assist') === 0) {
                assistPrefixMatch = name;
            }

        }

        return assistPrefixMatch;

    }

    async function loadAgents() {

        try {

            var response = await fetch('/api/agents');

            var data = await response.json();

            window._defaultAgentName = data.default_agent || '';
            agents = typeof window.sortAgentsAssistantFirst === 'function'
                ? window.sortAgentsAssistantFirst(data.agents || [], window._defaultAgentName)
                : (data.agents || []);

            // Also populate model badge
            var models = {};
            for (var i = 0; i < agents.length; i++) {
                models[agents[i].name] = agents[i];
            }
            window._agentModels = models;

            var select = document.getElementById('agentSelect');

            var previousAgent = select ? select.value : '';
            if (select) {
                select.replaceChildren();
                var defaultOption = document.createElement('option');
                defaultOption.value = '';
                defaultOption.textContent = chatText('composerDefaultAgent', '默认 Agent');
                defaultOption.setAttribute('data-i18n', 'composerDefaultAgent');
                select.appendChild(defaultOption);
                agents.forEach(function (agent) {
                    var option = document.createElement('option');
                    option.value = agent.name;
                    option.textContent = agent.name;
                    select.appendChild(option);
                });
                var createOption = document.createElement('option');
                var createValue = '__macs_create_agent__';
                while (agents.some(function (agent) { return agent.name === createValue; })) createValue += '_';
                createOption.value = createValue;
                createOption.dataset.action = 'create-agent';
                createOption.textContent = chatText('composerAddAgent', '＋自定义 Agent');
                createOption.setAttribute('data-i18n', 'composerAddAgent');
                select.appendChild(createOption);

                var previousAgentExists = false;
                for (var i = 0; i < agents.length; i++) {
                    if (agents[i].name === previousAgent) {
                        previousAgentExists = true;
                        break;
                    }
                }

                select.value = previousAgentExists ? previousAgent : preferredAgentName(agents);
            }
            if (typeof updateAgentModelBadge === 'function') updateAgentModelBadge();
            if (typeof window.updateComposerModelAgent === 'function') window.updateComposerModelAgent();

            // 更新协同Agent列表

            var collabList = document.getElementById('collabAgentsList');

            var collabHtml = '';

            for (var i = 0; i < agents.length; i++) {

                collabHtml += '<div class="agent-checkbox">' +

                    '<input type="checkbox" value="' + agents[i].name + '" id="agent_' + i + '" checked>' +

                    '<label for="agent_' + i + '">' + agents[i].name + ' (' + agents[i].type + ')</label>' +

                    '</div>';

            }

            collabList.innerHTML = collabHtml;

            addLog('info', '加载了 ' + agents.length + ' 个Agent');

        } catch (err) {

            console.error('加载 Agents 失败:', err);

            addLog('error', '加载Agents失败: ' + err.message);

        }

    }

    var sessionRecoveryCache = { sessionId: '', recoveries: [] };

    function chatText(key, fallback) {
        return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : (fallback || key);
    }

    var newConversationCloudLayout = [
        [23, 8, 150, -0.50, 3, -0.6], [42, 5, 148, 0.35, -3, -3.2], [61, 8, 166, -0.25, 2, -1.8], [80, 6, 142, 0.55, -2, -4.4],
        [12, 29, 154, 0.40, 2, -2.6], [31, 25, 160, -0.45, -3, -5.1], [50, 29, 144, 0.25, 3, -1.1], [69, 25, 154, -0.30, -2, -3.7], [88, 29, 148, 0.45, 2, -0.3],
        [7, 51, 132, -0.35, 3, -2.0], [24, 49, 151, 0.25, -3, -4.9], [41, 52, 140, -0.55, 2, -0.8], [58, 48, 136, 0.35, -2, -3.3], [75, 52, 145, -0.25, 3, -5.6], [93, 49, 132, 0.45, -3, -1.5],
        [13, 72, 148, -0.45, 2, -4.2], [32, 75, 140, 0.30, -2, -0.9], [51, 72, 156, -0.20, 3, -2.7], [70, 75, 158, 0.50, -3, -5.3], [89, 72, 144, -0.35, 2, -1.9],
        [23, 92, 140, 0.40, -2, -3.9], [43, 89, 136, -0.30, 3, -0.5], [62, 92, 142, 0.25, -3, -4.7], [81, 89, 140, -0.45, 2, -2.2]
    ];

    function newConversationPrompts() {
        var zh = [
            ['今天有哪些重要新闻', '帮我整理今天最值得关注的重要新闻，按主题归类并说明影响。'],
            ['财经和股票动态', '帮我总结今天的重要财经资讯、股票市场行情、行业板块和重大消息。'],
            ['做一份深度研究', '请围绕我接下来提供的主题做一份深度研究，列出关键事实、来源、结论和待验证问题。'],
            ['生成一份周报', '请根据我提供的工作记录整理本周周报，包含进展、成果、风险和下周计划。'],
            ['做一份汇报 PPT', '把我接下来提供的内容整理成一份结构清晰、可直接汇报的 PPT。'],
            ['分析这份表格', '请分析我上传的表格，检查数据质量、关键指标、趋势、异常和可执行结论。'],
            ['设计产品方案', '请把我的想法整理成产品方案，包含用户、场景、需求、流程、功能、风险和里程碑。'],
            ['拆解这项需求', '请把我接下来描述的需求拆成目标、范围、任务、依赖、验收标准和风险。'],
            ['规划项目里程碑', '请为这个项目制定里程碑计划，明确阶段目标、交付物、负责人和时间安排。'],
            ['设计系统架构', '请根据我的业务需求设计系统架构，说明模块、数据流、接口、安全和扩展方案。'],
            ['排查代码问题', '请帮我定位这段代码或这个项目中的问题，先分析原因，再给出可验证的修复方案。'],
            ['帮我写测试用例', '请根据我提供的功能或代码编写测试用例，覆盖正常、边界、异常和权限场景。'],
            ['审查这份合同', '请审查我提供的合同，识别关键条款、风险、缺失项和建议修改内容。'],
            ['整理会议纪要', '请把会议内容整理成纪要，提取结论、决策、待办、负责人和截止时间。'],
            ['Plan my project', 'Create a practical project plan with milestones, owners, dependencies, risks, and acceptance criteria.', 'en'],
            ['Summarize this document', 'Summarize the document I provide, preserve key facts, and extract decisions and action items.', 'en'],
            ['Analysez ce document', 'Analysez le document que je vais fournir, puis résumez les faits clés, les risques et les actions à mener.', 'fr'],
            ['Préparez une présentation', 'Transformez mon contenu en une présentation claire, structurée et prête à être présentée.', 'fr'],
            ['Проанализируй данные', 'Проанализируй мои данные, найди тенденции, аномалии и предложи конкретные следующие шаги.', 'ru'],
            ['Составь план проекта', 'Составь план проекта с этапами, ответственными, зависимостями, рисками и критериями приёмки.', 'ru'],
            ['Resuma este documento', 'Resuma o documento que eu fornecer, preserve os fatos principais e destaque decisões e próximos passos.', 'pt'],
            ['Crie um plano de marketing', 'Crie um plano de marketing com público, proposta de valor, canais, conteúdo e métricas.', 'pt'],
            ['Analiza esta hoja de cálculo', 'Analiza la hoja de cálculo que proporcionaré, identifica tendencias, anomalías y acciones recomendadas.', 'es'],
            ['Organiza esta reunión', 'Convierte el contenido de la reunión en decisiones, tareas, responsables y fechas límite.', 'es']
        ];
        if (window.openKittyLanguage !== 'en') return zh;
        return [
            ['Top news today', 'Summarize today’s most important news by topic and explain why it matters.'],
            ['Markets and stocks', 'Summarize today’s financial news, market moves, sectors, styles, and major events.'],
            ['Deep research', 'Research the topic I provide and organize key facts, sources, conclusions, and open questions.'],
            ['Create a weekly report', 'Turn my work notes into a weekly report with progress, outcomes, risks, and next steps.'],
            ['Build a presentation', 'Turn the material I provide into a clear presentation ready to deliver.'],
            ['Analyze this spreadsheet', 'Analyze my spreadsheet for data quality, metrics, trends, anomalies, and actions.'],
            ['Design a product plan', 'Turn my idea into a product plan covering users, scenarios, requirements, flow, risks, and milestones.'],
            ['Break down a requirement', 'Break my requirement into goals, scope, tasks, dependencies, acceptance criteria, and risks.'],
            ['Plan milestones', 'Create project milestones with goals, deliverables, owners, and dates.'],
            ['Design system architecture', 'Design a system architecture for my needs, including modules, data flow, APIs, security, and scaling.'],
            ['Debug code', 'Find the cause of this code or project issue and propose a verifiable fix.'],
            ['Write test cases', 'Write test cases for the feature or code I provide, including normal, edge, failure, and permission paths.'],
            ['Review a contract', 'Review my contract for key terms, risks, missing clauses, and suggested revisions.'],
            ['Create meeting notes', 'Turn the meeting into notes with decisions, action items, owners, and deadlines.'],
            ['Analysez ce document', 'Analysez le document que je vais fournir, puis résumez les faits clés, les risques et les actions à mener.', 'fr'],
            ['Préparez une présentation', 'Transformez mon contenu en une présentation claire, structurée et prête à être présentée.', 'fr'],
            ['Проанализируй данные', 'Проанализируй мои данные, найди тенденции, аномалии и предложи конкретные следующие шаги.', 'ru'],
            ['Составь план проекта', 'Составь план проекта с этапами, ответственными, зависимостями, рисками и критериями приёмки.', 'ru'],
            ['Resuma este documento', 'Resuma o documento que eu fornecer, preserve os fatos principais e destaque decisões e próximos passos.', 'pt'],
            ['Crie um plano de marketing', 'Crie um plano de marketing com público, proposta de valor, canais, conteúdo e métricas.', 'pt'],
            ['Analiza esta hoja de cálculo', 'Analiza la hoja de cálculo que proporcionaré, identifica tendencias, anomalías y acciones recomendadas.', 'es'],
            ['Organiza esta reunión', 'Convierte el contenido de la reunión en decisiones, tareas, responsables y fechas límite.', 'es'],
            ['整理这份文档', '请整理我上传的文档，保留关键事实，重组结构，并输出清晰摘要和行动项。', 'zh-CN'],
            ['做一份竞品分析', '请分析我提供的产品与主要竞品，从定位、功能、体验、价格和机会点进行比较。', 'zh-CN']
        ];
    }

    function renderNewConversationState() {
        var prompts = newConversationPrompts();
        var questions = '';
        for (var i = 0; i < prompts.length; i++) {
            var prompt = prompts[i];
            var layout = newConversationCloudLayout[i % newConversationCloudLayout.length];
            var style = '--cloud-x:' + layout[0] + '%;--cloud-y:' + layout[1] + '%;--cloud-w:' + layout[2] + 'px;--cloud-tilt:' + layout[3] + 'deg;--cloud-shift:' + layout[4] + 'px;--cloud-delay:' + layout[5] + 's;';
            questions += '<button type="button" class="new-conversation-question" style="' + style + '" data-message="' + escapeAttr(prompt[1]) + '" lang="' + escapeAttr(prompt[2] || (window.openKittyLanguage === 'en' ? 'en' : 'zh-CN')) + '" aria-pressed="false" onclick="useNewConversationPrompt(this)">' +
                '<span class="new-conversation-question-surface"><strong>' + escapeHtml(prompt[0]) + '</strong></span></button>';
        }
        return '<section class="new-conversation-state" id="newConversationState" aria-labelledby="newConversationHeading">' +
            '<h2 id="newConversationHeading">' + escapeHtml(chatText('newConversationHeading', '开始一段新对话')) + '</h2>' +
            '<p>' + escapeHtml(chatText('newConversationHint', '从问题云里挑一个，或直接写下你想完成的事')) + '</p>' +
            (questions ? '<div class="new-conversation-questions" aria-label="' + escapeAttr(chatText('commonQuestions', '常见提示词')) + '">' + questions + '</div>' : '') +
            '</section>';
    }

    window.renderNewConversationState = renderNewConversationState;

    function useNewConversationPrompt(button) {
        if (!button) return;
        var input = document.getElementById('messageInput');
        var message = button.getAttribute('data-message') || '';
        if (!input || !message) return;
        var siblings = button.parentNode ? button.parentNode.querySelectorAll('.new-conversation-question') : [];
        for (var i = 0; i < siblings.length; i++) {
            siblings[i].classList.toggle('is-selected', siblings[i] === button);
            siblings[i].setAttribute('aria-pressed', siblings[i] === button ? 'true' : 'false');
        }
        input.value = message;
        input.dispatchEvent(new Event('input', {bubbles: true}));
        input.focus({preventScroll: true});
        input.setSelectionRange(input.value.length, input.value.length);
        if (window.innerWidth <= 768) input.scrollIntoView({behavior: 'smooth', block: 'end'});
    }

    function clearConversationEmptyState(container) {
        if (!container) return;
        if (typeof window.setCommunityConversationState === 'function') window.setCommunityConversationState(true);
        var emptyStates = container.querySelectorAll('.new-conversation-state, .session-empty-state');
        for (var i = 0; i < emptyStates.length; i++) emptyStates[i].remove();
    }

    window.useNewConversationPrompt = useNewConversationPrompt;

    function renderSessionRecoveryPanel(recoveries) {
        recoveries = Array.isArray(recoveries) ? recoveries.filter(function(item) {
            return item && item.can_resume && item.task_id;
        }) : [];
        if (recoveries.length === 0) return '';

        var html = '<section class="session-recovery-panel" id="sessionRecoveryPanel" aria-live="polite">' +
            '<div class="session-recovery-heading"><div><strong>' + escapeHtml(chatText('recoveryTitle', '发现可恢复的中断任务')) + '</strong>' +
            '<p>' + escapeHtml(chatText('recoveryHint', 'MACS 已保存退出前的执行日志。继续前会先核对当前状态，不会盲目重复有副作用的操作。')) + '</p></div></div>';
        for (var i = 0; i < recoveries.length; i++) {
            var recovery = recoveries[i];
            var original = recovery.original_request_preview || '';
            var progress = recovery.progress_preview || '';
            var count = Number(recovery.entry_count || 0);
            var time = '';
            if (recovery.last_activity) {
                var date = new Date(recovery.last_activity);
                if (!isNaN(date.getTime())) {
                    time = date.toLocaleString(window.openKittyLanguage === 'en' ? 'en-US' : 'zh-CN');
                }
            }
            html += '<article class="session-recovery-card">' +
                '<div class="session-recovery-copy">' +
                (original ? '<div><span>' + escapeHtml(chatText('recoveryOriginal', '原始请求')) + '：</span>' + escapeHtml(original) + '</div>' : '') +
                (progress ? '<div><span>' + escapeHtml(chatText('recoveryProgress', '最后进度')) + '：</span>' + escapeHtml(progress) + '</div>' : '') +
                '<small>' + (count ? escapeHtml(String(count) + ' ' + chatText('recoveryEntryCount', '日志条目')) : '') +
                (count && time ? ' · ' : '') + escapeHtml(time) + '</small></div>' +
                '<button type="button" class="session-recovery-resume" data-recovery-task="' + escapeHtml(String(recovery.task_id)) + '">' +
                escapeHtml(chatText('recoveryResume', '安全继续')) + '</button></article>';
        }
        return html + '</section>';
    }

    function bindSessionRecoveryActions(container) {
        if (!container) return;
        var buttons = container.querySelectorAll('[data-recovery-task]');
        for (var i = 0; i < buttons.length; i++) {
            buttons[i].addEventListener('click', function() {
                resumeInterruptedTaskFromWeb(this.getAttribute('data-recovery-task') || '', this);
            });
        }
    }

    function refreshSessionRecoveryLanguage() {
        if (!sessionRecoveryCache.sessionId || sessionRecoveryCache.sessionId !== currentSessionId) return;
        var current = document.getElementById('sessionRecoveryPanel');
        if (!current) return;
        var holder = document.createElement('div');
        holder.innerHTML = renderSessionRecoveryPanel(sessionRecoveryCache.recoveries);
        var replacement = holder.firstElementChild;
        if (!replacement) {
            current.remove();
            return;
        }
        current.parentNode.replaceChild(replacement, current);
        bindSessionRecoveryActions(replacement);
    }

    function refreshNewConversationLanguage() {
        var current = document.getElementById('newConversationState');
        if (!current) return;
        var holder = document.createElement('div');
        holder.innerHTML = renderNewConversationState();
        var replacement = holder.firstElementChild;
        if (replacement) current.parentNode.replaceChild(replacement, current);
    }

    async function resumeInterruptedTaskFromWeb(taskId, button) {
        if (!taskId || !button || button.disabled) return;
        var sessionAtStart = currentSessionId;
        button.disabled = true;
        button.textContent = chatText('recoveryResuming', '正在恢复…');
        try {
            var response = await fetch('/api/task/resume', {
                method: 'POST',
                headers: {'Content-Type': 'application/json; charset=utf-8', 'Accept': 'application/json'},
                body: JSON.stringify({task_id: taskId})
            });
            var result = await response.json().catch(function() { return {}; });
            if (!response.ok) throw new Error(result.error || ('HTTP ' + response.status));
            if (typeof showToast === 'function') showToast(chatText('recoveryStarted', '恢复任务已启动'), 'success');
            if (result.task_id && sessionAtStart) {
                window.taskSessions = window.taskSessions || {};
                window.taskSessions[result.task_id] = sessionAtStart;
            }
            if (currentSessionId === sessionAtStart) {
                await loadSession(sessionAtStart);
                if (typeof restorePendingTasks === 'function') {
                    setTimeout(function() { restorePendingTasks(sessionAtStart); }, 50);
                }
            }
        } catch (err) {
            button.disabled = false;
            button.textContent = chatText('recoveryResume', '安全继续');
            if (typeof showToast === 'function') showToast(chatText('recoveryFailed', '恢复任务失败') + ': ' + err.message, 'error');
            else alert(chatText('recoveryFailed', '恢复任务失败') + ': ' + err.message);
        }
    }
    window.resumeInterruptedTaskFromWeb = resumeInterruptedTaskFromWeb;
    document.addEventListener('openkitty:languagechange', refreshSessionRecoveryLanguage);
    document.addEventListener('openkitty:languagechange', refreshNewConversationLanguage);

    async function loadSession(sessionId) {
		window.stopGenerationPolling?.();
		if (typeof window.setCommunityConversationState === 'function') window.setCommunityConversationState(true);

		currentSessionId = sessionId;

		if (typeof rememberActiveSessionId === 'function') rememberActiveSessionId(sessionId);
			try { localStorage.removeItem('lastCompletedTaskId'); localStorage.removeItem('lastDuration'); } catch(e) {}

		// 判断会话来源

		if (sessionId.startsWith('web_')) {

			currentSessionSource = 'web';

		} else if (sessionId.startsWith('qq_')) {

			currentSessionSource = 'qq';

		} else if (sessionId.startsWith('feishu_')) {

			currentSessionSource = 'feishu';

		} else if (sessionId.startsWith('dingtalk_')) {

			currentSessionSource = 'dingtalk';

		} else if (sessionId.startsWith('wework_')) {

			currentSessionSource = 'wework';

		} else {

			currentSessionSource = 'other';

		}

		// 更新输入区域状态

		updateInputAreaState();

		try {

			var response = await fetch('/api/session/' + encodeURIComponent(sessionId));
			if (!response.ok) throw new Error('HTTP ' + response.status);

			var data = await response.json();
			if (currentSessionId !== sessionId) return;

			var messages = data.messages || [];
			var recoveries = data.recoveries || [];
			sessionRecoveryCache = {sessionId: sessionId, recoveries: recoveries};
			var recoveryHTML = renderSessionRecoveryPanel(recoveries);

			// 保存会话耗时供注入使用

	
			var container = document.getElementById('messagesArea');

			if (messages.length === 0) {

				container.innerHTML = recoveryHTML + (recoveries.length ? '<div class="session-empty-state">' + escapeHtml(chatText('startConversation', '开始对话吧！')) + '</div>' : renderNewConversationState());
				if (typeof window.setCommunityConversationState === 'function') window.setCommunityConversationState(false);
				bindSessionRecoveryActions(container);
				setSessionHeaderTitle(chatText('newConversation', '新对话'));

			document.getElementById('welcomeTips') && (document.getElementById('welcomeTips').style.display = recoveries.length ? 'none' : '');
				loadSessions();

				return;

			}

			var html = '';

			for (var i = 0; i < messages.length; i++) {

				html += renderMessage(messages[i]);

			}

			container.innerHTML = recoveryHTML + html;
			window.resumeGenerationMessages?.(messages, sessionId);
			bindSessionRecoveryActions(container);
			document.getElementById('welcomeTips') && (document.getElementById('welcomeTips').style.display = 'none');

			// 追加任务耗时到最后一个助手气泡

			// 注入耗时：SSE 精确值
			var taskDuration = window._lastDuration;

			if (taskDuration) {

				var infos = container.querySelectorAll('.message.assistant .message-info');

				if (infos.length > 0) {
					infos[infos.length - 1].innerHTML += ' ⏱ ' + taskDuration.replace(/s$/,"") + 's';
				}

	
			delete window._handledTask;

			}

			scrollToBottom();

			// 更新会话标题（可选）

			var sessionTitle = document.getElementById('sessionTitle');

			if (sessionTitle && messages.length > 0) {

				// 使用第一条用户消息作为标题预览

				var firstUserMsg = messages.find(function(m) { return m.role === 'user'; });

				if (firstUserMsg && firstUserMsg.content) {

					var shortTitle = firstUserMsg.content.substring(0, 25);
					if (firstUserMsg.content.length > 25) shortTitle += '...';
					setSessionHeaderTitle(shortTitle);

				} else {

					setSessionHeaderTitle('MACS');

				}

			}

			loadSessions();

			addLog('info', '加载会话: ' + sessionId + ' (来源: ' + currentSessionSource + ', 消息数: ' + messages.length + ')');

		} catch (err) {

			console.error('加载消息失败:', err);

			addLog('error', '加载消息失败: ' + err.message);

		}

	}

	window.loadSession = loadSession;

    function copySessionId(id) {
        var fallback = function() {
            var ta = document.createElement('textarea');
            ta.value = id;
            ta.style.position = 'fixed';
            ta.style.left = '-9999px';
            document.body.appendChild(ta);
            ta.select();
            document.execCommand('copy');
            document.body.removeChild(ta);
            showToast('已复制: ' + id.substring(0, 24) + (id.length > 24 ? '...' : ''));
        };
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(id).then(function() {
                showToast('已复制: ' + id.substring(0, 24) + (id.length > 24 ? '...' : ''));
            }).catch(fallback);
        } else {
            fallback();
        }
    }

    function copyCurrentSessionId() {
        if (currentSessionId) {
            copySessionId(currentSessionId);
        } else {
            showToast('当前无会话');
        }
    }

	function renderInlineImages(text) {
		var images = [];
		var dataUrlRegex = /data:image\/[^;]+;base64,[a-zA-Z0-9+\/=]+/g;
		var match;
		var plain = text;
		while ((match = dataUrlRegex.exec(text)) !== null) {
			images.push('<img src="' + match[0] + '" style="max-width:300px;max-height:300px;border-radius:8px;margin:4px 0;display:block;cursor:pointer;" onclick="window.open(this.src)" title="点击查看原图">');
			plain = plain.replace(match[0], '');
		}
		return { images: images, plain: plain };
	}


    function renderArtifacts(html) {
        var artifactRegex = /<pre><code class="language-(html|svg|mermaid|artifact:html|artifact:svg|artifact:mermaid)">([\s\S]*?)<\/code><\/pre>/gi;
        return html.replace(artifactRegex, function(match, lang, code) {
            var decoded = code
                .replace(/&lt;/g, '<')
                .replace(/&gt;/g, '>')
                .replace(/&amp;/g, '&')
                .replace(/&quot;/g, '"')
                .replace(/&#39;/g, "'");
            var cleanLang = lang.replace('artifact:', '');
            var id = 'artifact_' + Date.now() + '_' + Math.random().toString(36).substr(2,6);

            if (cleanLang === 'html') {
                return '<div class="artifact-widget">' +
                    '<div class="artifact-header" onclick="toggleArtifact(\'' + id + '\')">' +
                    '<span>\u{1f310} HTML Preview</span><span class="artifact-toggle">▼</span></div>' +
                    '<iframe id="' + id + '" class="artifact-iframe" srcdoc="' + encodeURIComponent(decoded) + '" sandbox="allow-scripts" loading="lazy"></iframe>' +
                    '</div>';
            } else if (cleanLang === 'svg') {
                var clean = decoded.replace(/<script[\s\S]*?<\/script>/gi, '')
                    .replace(/on\w+\s*=\s*"[^"]*"/gi, '')
                    .replace(/on\w+\s*=\s*'[^']*'/gi, '');
                return '<div class="artifact-widget">' +
                    '<div class="artifact-header" onclick="toggleArtifact(\'' + id + '\')">' +
                    '<span>\u{1f3a8} SVG Preview</span><span class="artifact-toggle">▼</span></div>' +
                    '<div id="' + id + '" class="artifact-content">' + clean + '</div>' +
                    '</div>';
            } else if (cleanLang === 'mermaid') {
                return '<div class="artifact-widget">' +
                    '<div class="artifact-header" onclick="toggleArtifact(\'' + id + '\')">' +
                    '<span>\u{1f4ca} Mermaid Diagram</span><span class="artifact-toggle">▼</span></div>' +
                    '<div id="' + id + '" class="artifact-content mermaid-code">' + escapeHtml(decoded) + '</div>' +
                    '</div>';
            }
            return match;
        });
    }


    // Render mermaid diagrams after DOM update
    window.renderMermaidDiagrams = function() {
        if (typeof mermaid === 'undefined') return;
        var srcs = document.querySelectorAll('.mermaid-src,.mermaid-code');
        for (var i = 0; i < srcs.length; i++) {
            (function(el) {
                var code = el.textContent || el.innerText;
                var svgId = 'msvg_' + Math.random().toString(36).substr(2,8);
                mermaid.render(svgId, code).then(function(r) {
                    el.innerHTML = r.svg;
                    el.className = 'mermaid-rendered';
                }).catch(function() {});
            })(srcs[i]);
        }
    };
    setTimeout(window.renderMermaidDiagrams, 200);
    window.toggleArtifact = function(id) {
        var el = document.getElementById(id);
        if (!el) return;
        var header = el.previousElementSibling;
        var arrow = header ? header.querySelector('.artifact-toggle') : null;
        if (el.style.display === 'none') {
            el.style.display = '';
            if (arrow) arrow.textContent = '▼';
        } else {
            el.style.display = 'none';
            if (arrow) arrow.textContent = '▶';
        }
    };

	function enhanceMessageImages(html) {
		var host = document.createElement('div');
		host.innerHTML = html || '';
		var imgs = host.querySelectorAll('img');
		for (var i = 0; i < imgs.length; i++) {
			var img = imgs[i];
			if (img.closest && img.closest('.artifact-widget')) continue;
			var src = img.getAttribute('src') || '';
			if (!src) continue;
			img.classList.add('message-image-thumb');
			img.setAttribute('data-openkitty-image-preview', '1');
			img.setAttribute('loading', 'lazy');
			img.setAttribute('decoding', 'async');
			img.setAttribute('role', 'button');
			img.setAttribute('tabindex', '0');
			img.removeAttribute('onclick');
			if (!img.getAttribute('alt')) img.setAttribute('alt', 'image');
			if (!img.getAttribute('title')) img.setAttribute('title', 'Click to view full image');
			var parent = img.parentElement;
			if (parent && parent.tagName === 'A') {
				parent.classList.add('message-image-link');
				parent.setAttribute('data-openkitty-image-preview-link', '1');
				if (!parent.getAttribute('title')) parent.setAttribute('title', 'Click to view full image');
			}
		}
		return host.innerHTML;
	}

	function imagePreviewTitleFromSrc(src, alt) {
		var title = (alt || '').trim();
		if (title && title !== 'image') return title;
		try {
			var url = new URL(src, window.location.href);
			var path = url.searchParams.get('path') || url.pathname || '';
			var parts = decodeURIComponent(path).replace(/\\/g, '/').split('/');
			return parts[parts.length - 1] || 'image';
		} catch (e) {
			return 'image';
		}
	}

	function imagePreviewFilename(src, title) {
		var name = (title || '').trim();
		if (name && name !== 'image') return name;
		try {
			var url = new URL(src, window.location.href);
			var path = url.searchParams.get('path') || url.pathname || '';
			var parts = decodeURIComponent(path).replace(/\\/g, '/').split('/').filter(Boolean);
			return parts[parts.length - 1] || 'image';
		} catch (e) {
			var fallback = String(src || '').split(/[\\/]/).filter(Boolean).pop();
			return fallback || 'image';
		}
	}

	function downloadMessageImage(src, title, button) {
		if (!src) return;
		var filename = imagePreviewFilename(src, title);
		var originalText = button ? button.textContent : '';
		if (button) {
			button.disabled = true;
			button.textContent = '保存中';
		}
		function finish() {
			if (button) {
				button.disabled = false;
				button.textContent = originalText || '下载';
			}
		}
		function directDownload(url) {
			var link = document.createElement('a');
			link.href = url;
			link.download = filename;
			link.rel = 'noopener noreferrer';
			document.body.appendChild(link);
			link.click();
			link.remove();
		}
		if (/^data:/i.test(src)) {
			directDownload(src);
			finish();
			return;
		}
		fetch(src)
			.then(function(resp) {
				if (!resp.ok) throw new Error('HTTP ' + resp.status);
				return resp.blob();
			})
			.then(function(blob) {
				var blobUrl = URL.createObjectURL(blob);
				directDownload(blobUrl);
				setTimeout(function() { URL.revokeObjectURL(blobUrl); }, 30000);
			})
			.catch(function() {
				window.open(src, '_blank');
			})
			.finally(finish);
	}

	function openMessageImagePreview(src, title) {
		if (!src) return;
		var modal = document.getElementById('previewModal');
		var previewBody = document.getElementById('previewBody');
		var previewTitle = document.getElementById('previewTitle');
		if (!modal || !previewBody || !previewTitle) {
			window.open(src, '_blank');
			return;
		}
		modal.classList.add('image-preview-active');
		previewTitle.textContent = title || 'image';
		previewBody.innerHTML = '';
		var toolbar = document.createElement('div');
		toolbar.className = 'image-preview-toolbar';
		var zoomOut = document.createElement('button');
		zoomOut.type = 'button';
		zoomOut.textContent = '-';
		zoomOut.title = 'Zoom out';
		var zoomLabel = document.createElement('button');
		zoomLabel.type = 'button';
		zoomLabel.textContent = '适应';
		zoomLabel.title = '适应窗口';
		var zoomIn = document.createElement('button');
		zoomIn.type = 'button';
		zoomIn.textContent = '+';
		zoomIn.title = 'Zoom in';
		var actualSize = document.createElement('button');
		actualSize.type = 'button';
		actualSize.textContent = '100%';
		actualSize.title = '原始尺寸';
		var downloadButton = document.createElement('button');
		downloadButton.type = 'button';
		downloadButton.textContent = '下载';
		downloadButton.title = '下载图片';
		var openOriginal = document.createElement('a');
		openOriginal.href = src;
		openOriginal.target = '_blank';
		openOriginal.rel = 'noopener';
		openOriginal.textContent = '打开';
		openOriginal.title = '打开原图';
		toolbar.appendChild(zoomOut);
		toolbar.appendChild(zoomLabel);
		toolbar.appendChild(zoomIn);
		toolbar.appendChild(actualSize);
		toolbar.appendChild(downloadButton);
		toolbar.appendChild(openOriginal);
		var wrap = document.createElement('div');
		wrap.className = 'image-preview-wrap is-fit';
		var img = document.createElement('img');
		img.className = 'image-preview-full';
		img.src = src;
		img.alt = title || 'image';
		wrap.appendChild(img);
		var fallback = document.createElement('div');
		fallback.className = 'image-preview-fallback';
		fallback.innerHTML = '<div>图片预览失败</div>';
		var fallbackLink = document.createElement('a');
		fallbackLink.href = src;
		fallbackLink.target = '_blank';
		fallbackLink.rel = 'noopener';
		fallbackLink.textContent = '打开原图';
		fallback.appendChild(fallbackLink);
		previewBody.appendChild(toolbar);
		previewBody.appendChild(wrap);
		modal.classList.add('active');

		var scale = 1;
		var isFit = true;
		function clampScale(next) {
			return Math.max(0.1, Math.min(8, next));
		}
		function setZoom(nextScale, fit) {
			isFit = !!fit;
			if (isFit) {
				img.style.width = '';
				img.style.height = '';
				img.style.maxWidth = '';
				img.style.maxHeight = '';
				img.classList.remove('is-zoomed');
				wrap.classList.add('is-fit');
				zoomLabel.textContent = '适应';
				return;
			}
			scale = clampScale(nextScale);
			var naturalWidth = img.naturalWidth || img.width || 1;
			img.style.width = Math.max(1, Math.round(naturalWidth * scale)) + 'px';
			img.style.height = 'auto';
			img.style.maxWidth = 'none';
			img.style.maxHeight = 'none';
			img.classList.add('is-zoomed');
			wrap.classList.remove('is-fit');
			zoomLabel.textContent = Math.round(scale * 100) + '%';
		}
		function zoomBy(delta) {
			setZoom((isFit ? 1 : scale) + delta, false);
		}
		zoomOut.addEventListener('click', function() { zoomBy(-0.25); });
		zoomIn.addEventListener('click', function() { zoomBy(0.25); });
		actualSize.addEventListener('click', function() { setZoom(1, false); });
		zoomLabel.addEventListener('click', function() { setZoom(1, true); });
		downloadButton.addEventListener('click', function() {
			downloadMessageImage(src, title || '', downloadButton);
		});
		img.addEventListener('dblclick', function() {
			if (isFit) setZoom(1, false);
			else setZoom(1, true);
		});
		wrap.addEventListener('wheel', function(e) {
			if (!e.ctrlKey && !e.metaKey) return;
			e.preventDefault();
			zoomBy(e.deltaY > 0 ? -0.25 : 0.25);
		}, { passive: false });
		img.addEventListener('load', function() {
			if (!isFit) setZoom(scale, false);
		});
		img.addEventListener('error', function() {
			wrap.replaceWith(fallback);
		});
	}
	window.openMessageImagePreview = openMessageImagePreview;

	function bindMessageImagePreviewEvents() {
		if (window.__openkittyImagePreviewBound) return;
		window.__openkittyImagePreviewBound = true;
		document.addEventListener('click', function(e) {
			var img = e.target && e.target.closest ? e.target.closest('img[data-openkitty-image-preview="1"]') : null;
			if (!img) return;
			e.preventDefault();
			e.stopPropagation();
			openMessageImagePreview(img.currentSrc || img.getAttribute('src'), imagePreviewTitleFromSrc(img.getAttribute('src') || '', img.getAttribute('alt') || ''));
		});
		document.addEventListener('keydown', function(e) {
			if (e.key !== 'Enter' && e.key !== ' ') return;
			var img = e.target && e.target.matches && e.target.matches('img[data-openkitty-image-preview="1"]') ? e.target : null;
			if (!img) return;
			e.preventDefault();
			openMessageImagePreview(img.currentSrc || img.getAttribute('src'), imagePreviewTitleFromSrc(img.getAttribute('src') || '', img.getAttribute('alt') || ''));
		});
	}
	bindMessageImagePreviewEvents();

	// Detailed execution metadata belongs in diagnostics, not in the ordinary
	// conversation. The browser-local debug toggle is the explicit opt-in for
	// model names, progress logs and raw execution events.
	function isDetailedExecutionUIEnabled() {
		try {
			return typeof isDebugMode === 'function' && isDebugMode();
		} catch (e) {
			return false;
		}
	}
	window.isDetailedExecutionUIEnabled = isDetailedExecutionUIEnabled;

	function internalExecutionSignalCount(value) {
		var text = String(value || '');
		var signals = [
			/Tool call rejected/i,
			/TOOL SCHEMA REPAIR/i,
			/retry-guard/i,
			/\buse_skill\b/i,
			/\bcode_check\b/i,
			/\bptc_execute\b/i,
			/\bOPENKITTY_(?:ACTION|SOURCE)_RESULT\b/i,
			/\.openkitty[\\/](?:runtime|\.system|logs?|cache|tmp)[\\/]/i,
			/\b(?:http_status|prepared_sha256|content_sha256)\b/i,
			/^\s*Traceback\s*\(/im,
			/^\s*at\s+\S+\s+\([^\n]+:\d+/im
		];
		var count = 0;
		for (var i = 0; i < signals.length; i++) {
			if (signals[i].test(text)) count++;
		}
		return count;
	}

	function looksLikeInternalExecutionReport(value) {
		var text = String(value || '').trim();
		if (!text) return false;
		var failedEnvelope = /^(?:\u26a0\ufe0f?\s*)?MACS\s*\u6267\u884c\u5931\u8d25/i.test(text);
		var intakeEnvelope = /^\s*\[[^\]\n]{0,160}(?:\u6267\u884c\u7b80\u62a5|execution brief)[^\]\n]*\]/i.test(text);
		var signalCount = internalExecutionSignalCount(text);
		return (failedEnvelope && signalCount >= 1) || intakeEnvelope || signalCount >= 4;
	}
	window.looksLikeInternalExecutionReport = looksLikeInternalExecutionReport;

	function sanitizeAssistantBubbleContent(value) {
		var original = String(value || '').replace(/\r\n?/g, '\n');
		if (!original.trim()) return '';
		if (looksLikeInternalExecutionReport(original)) {
			return '\u4efb\u52a1\u672a\u5b8c\u5168\u5b8c\u6210\uff0c\u8bf7\u91cd\u8bd5\u3002';
		}

		// Keep deliverable paths long enough for the artifact linkifier to turn
		// them into filename-only download chips. Other runtime paths are removed.
		var text = normalizeArtifactPathsForLinkify(original);
		text = text.replace(/\.openkitty[\\/](?:runtime|\.system|logs?|cache|tmp)[\\/][^\s`"'<>)]*/gi, '[\u5185\u90e8\u8def\u5f84]');
		text = text.replace(/[A-Za-z]:[\\/][^\s`"'<>)]*openkitty[\\/][^\s`"'<>)]*/gi, '[\u5185\u90e8\u8def\u5f84]');
		text = text.replace(/\/(?:home\/[^/\s]+|root|workspace|tmp)\/[^\s`"'<>)]*openkitty[^\s`"'<>)]*/gi, '[\u5185\u90e8\u8def\u5f84]');

		var omitted = false;
		var kept = [];
		var lines = text.split('\n');
		var internalLine = /^\s*(?:\d{1,2}:\d{2}:\d{2}\s+)?(?:use_skill|code_check|ptc_execute|tool_call|Tool call rejected|\[TOOL SCHEMA REPAIR\]|\[FAIL\]\s*(?:Run|Tool)|Traceback\b|at\s+\S+\s+\([^\n]+:\d+|OPENKITTY_(?:ACTION|SOURCE)_RESULT\s*=)/i;
		for (var i = 0; i < lines.length; i++) {
			if (internalLine.test(lines[i]) || /retry-guard/i.test(lines[i])) {
				omitted = true;
				continue;
			}
			kept.push(lines[i]);
		}
		text = kept.join('\n').replace(/```\s*```/g, '').replace(/\n{3,}/g, '\n\n').trim();
		if (!text && omitted) return '\u4efb\u52a1\u672a\u5b8c\u5168\u5b8c\u6210\uff0c\u8bf7\u91cd\u8bd5\u3002';
		return text;
	}
	window.sanitizeAssistantBubbleContent = sanitizeAssistantBubbleContent;

	function buildProgressLogLinkHtml(taskId, sessionId, style) {
		if (!isDetailedExecutionUIEnabled()) return '';
		taskId = (taskId || '').toString().trim();
		if (!taskId) return '';
		if (!sessionId && typeof currentSessionId !== 'undefined') sessionId = currentSessionId || '';
		var resolvedTaskId = window.resolveProgressLogTaskId ? window.resolveProgressLogTaskId(taskId, sessionId) : taskId;
		if (!resolvedTaskId) return '';
		var href = window.progressLogDownloadUrl
			? window.progressLogDownloadUrl(taskId, sessionId)
			: '/api/task/progress-log/' + encodeURIComponent(resolvedTaskId);
		style = style || 'margin-left:8px;color:#28a745;text-decoration:none;font-size:calc(12.1px * var(--macs-font-scale, 1));';
		return '<a href="' + escapeHtml(href) + '" download="progress_' + escapeHtml(resolvedTaskId) + '.log" style="' + escapeHtml(style) + '">&#x4e0b;&#x8f7d;&#x65e5;&#x5fd7;</a>';
	}
	window.buildProgressLogLinkHtml = buildProgressLogLinkHtml;

	function normalizedMessageAttachmentKind(attachment) {
		var declared = String(attachment && attachment.kind || '').toLowerCase();
		var mimeType = String(attachment && (attachment.mime_type || attachment.type) || '').toLowerCase();
		var path = String(attachment && (attachment.path || attachment.file_path) || '').toLowerCase();
		if (mimeType.indexOf('image/svg+xml') === 0 || /\.svg$/.test(path)) return 'file';
		if (declared === 'image' || mimeType.indexOf('image/') === 0 || /\.(png|jpe?g|gif|webp|bmp|avif)$/.test(path)) return 'image';
		return 'file';
	}

	function normalizeMessageAttachments(msg) {
		var raw = msg && (msg.attachments || msg.files || msg.attachment);
		if (typeof raw === 'string') {
			try { raw = JSON.parse(raw); } catch (e) { raw = []; }
		}
		if (!Array.isArray(raw)) raw = raw ? [raw] : [];
		if (msg && msg.file_path) {
			raw.push({path: msg.file_path, name: msg.file_name || ''});
		}
		var seen = {};
		return raw.map(function(attachment) {
			attachment = attachment || {};
			var path = String(attachment.path || attachment.file_path || '').trim().replace(/\\/g, '/');
			if (!path || seen[path]) return null;
			seen[path] = true;
			var name = String(attachment.name || attachment.filename || path.split('/').pop() || '附件');
			return {
				name: name,
				path: path,
				mime_type: String(attachment.mime_type || attachment.type || ''),
				size: Number(attachment.size || 0),
				kind: normalizedMessageAttachmentKind(attachment)
			};
		}).filter(Boolean);
	}

	function extractLegacyUserAttachment(content, attachments) {
		if (attachments.length > 0) return {content: content, attachments: attachments};
		var match = String(content || '').match(/^📎\s*\[用户上传了文件:\s*(.*?),\s*路径:\s*(.*?)\]\s*\n请先读取并分析这个文件的内容，然后处理用户的请求。\s*\n+用户消息:\s*\n?([\s\S]*)$/);
		if (!match) return {content: content, attachments: attachments};
		return {
			content: match[3],
			attachments: [{
				name: match[1].trim(),
				path: match[2].trim().replace(/\\/g, '/'),
				mime_type: '',
				size: 0,
				kind: normalizedMessageAttachmentKind({path: match[2]})
			}]
		};
	}

	function formatMessageAttachmentSize(size) {
		size = Number(size || 0);
		if (!size) return '';
		if (size < 1024) return size + ' B';
		if (size < 1024 * 1024) return Math.round(size / 1024) + ' KB';
		return (size / (1024 * 1024)).toFixed(size < 10 * 1024 * 1024 ? 1 : 0) + ' MB';
	}

	function renderMessageAttachments(attachments) {
		if (!attachments || attachments.length === 0) return '';
		var html = '<div class="message-attachments" data-openkitty-attachments="' + attachments.length + '">';
		for (var i = 0; i < attachments.length; i++) {
			var attachment = attachments[i];
			var name = escapeHtml(attachment.name || '附件');
			var size = formatMessageAttachmentSize(attachment.size);
			var downloadURL = buildFileDownloadUrl(attachment.path);
			if (attachment.kind === 'image') {
				var imageURL = buildFileInlineUrl(attachment.path);
				html += '<figure class="message-attachment message-attachment-image">' +
					'<img src="' + escapeHtml(imageURL) + '" alt="' + name + '" title="点击查看原图">' +
					'<figcaption><span>' + name + '</span>' + (size ? '<small>' + escapeHtml(size) + '</small>' : '') +
					'<a href="' + escapeHtml(downloadURL) + '" download aria-label="下载 ' + name + '">下载</a></figcaption>' +
					'</figure>';
			} else {
				var previewURL = buildFileViewUrl(attachment.path);
				var extension = (attachment.name.split('.').pop() || 'FILE').toUpperCase().substring(0, 6);
				html += '<div class="message-attachment message-attachment-file">' +
					'<a class="message-attachment-main" href="' + escapeHtml(previewURL) + '" target="_blank" rel="noopener" title="打开 ' + name + '">' +
					'<span class="message-attachment-icon">' + escapeHtml(extension) + '</span>' +
					'<span class="message-attachment-details"><strong>' + name + '</strong>' +
					(size ? '<small>' + escapeHtml(size) + '</small>' : '') + '</span></a>' +
					'<a class="message-attachment-download" href="' + escapeHtml(downloadURL) + '" download aria-label="下载 ' + name + '">下载</a>' +
					'</div>';
			}
		}
		return html + '</div>';
	}

	function renderMessage(msg) {
		if (msg.generation && typeof window.renderGenerationMessage === 'function') return window.renderGenerationMessage(msg);

		var isUser = msg.role === 'user';

		var rawContent = String(msg.content || '');
		var messageAttachments = normalizeMessageAttachments(msg);
		if (isUser) {
			var legacyEnvelope = extractLegacyUserAttachment(rawContent, messageAttachments);
			rawContent = legacyEnvelope.content;
			messageAttachments = legacyEnvelope.attachments;
		}

		// 先解码 HTML 实体
		var decodedContent = rawContent
			.replace(/&#39;/g, "'")
			.replace(/&quot;/g, '"')
			.replace(/&lt;/g, '<')
			.replace(/&gt;/g, '>')
			.replace(/&amp;/g, '&');

		var time = formatDate(msg.created_at);
		var taskId = msg.task_id || msg.taskId || '';
		var logLink = (!isUser && taskId && isDetailedExecutionUIEnabled()) ? (' ' + buildProgressLogLinkHtml(taskId, msg.session_id || '')) : '';
		var agentName = msg.agent_name || (isUser ? '我' : 'AI助手');

		// 渠道标识
		var channelBadge = '';
		if (msg.channel && msg.channel !== 'web') {
			var channelIcons = {'qq': '🐧', 'feishu': '🐦', 'dingtalk': '📱', 'wework': '💼'};
			var icon = channelIcons[msg.channel] || '📡';
			channelBadge = '<span style="font-size:calc(11px * var(--macs-font-scale, 1)); background:#e9ecef; padding:2px 6px; border-radius:10px; margin-left:8px;">' + icon + ' ' + msg.channel + '</span>';
		}

		var renderedContent;

		if (isUser) {
			// User messages: render inline images, then plain text
			var imgResult = renderInlineImages(decodedContent);
			renderedContent = escapeHtml(imgResult.plain).replace(/\n/g, '<br>');
			if (imgResult.images.length > 0) {
				renderedContent = imgResult.images.join('') + renderedContent;
			}
		} else if (decodedContent.trim().startsWith('{') && decodedContent.trim().endsWith('}')) {
			// JSON content (collaboration results) — render as card, skip path linkify
			renderedContent = (renderCollaborationResult(decodedContent) || renderCollabFallback(decodedContent));
		} else {
			// Assistant messages: user-facing markdown + filename-only artifacts.
			decodedContent = sanitizeAssistantBubbleContent(decodedContent);
			renderedContent = renderAsMarkdown(normalizeArtifactPathsForLinkify(decodedContent));
			renderedContent = makePathsClickable(renderedContent);
			renderedContent = enhanceMessageImages(renderedContent);
		}

		renderedContent = enhanceMessageImages(renderedContent);
		var renderedAttachments = enhanceMessageImages(renderMessageAttachments(messageAttachments));

		var avatarHtml = '';
		var editButton = isUser ? '<button class="message-edit-btn" title="编辑这条消息" aria-label="编辑这条消息" data-message="' + encodeURIComponent(decodedContent) + '" onclick="editUserMessage(this)">✎</button>' : '';

		return '<div class="message ' + msg.role + '">' +
			avatarHtml +
			editButton +
			'<div class="message-bubble">' +
			(!isUser ? '<div class="agent-name">' + escapeHtml(agentName) + channelBadge + '</div>' + renderMessageModelBadge(agentName) : '') +
			'<div class="message-content">' + renderedContent + renderedAttachments + '</div>' +
			'<div class="message-info">' + time + logLink + '</div>' +
			'</div>' +
			'</div>';
	}

	function renderMessageModelBadge(agentName) {
		if (!isDetailedExecutionUIEnabled()) return '';
		var agents = window._agentModels || {};
		var model = agents[agentName];
		if (!model || !model.model) return '';
		var configName = model.model_config || '';
		var label = model.model;
		if (configName && configName !== model.model) {
			label = configName + ' / ' + model.model;
		}
		if (model.max_tokens >= 1000000) {
			label += ' [' + Math.round(model.max_tokens / 1000000) + 'M]';
		} else if (model.max_tokens >= 1000) {
			label += ' [' + Math.round(model.max_tokens / 1000) + 'K]';
		}
		return '<div class="message-model-badge" style="font-size:calc(9.9px * var(--macs-font-scale, 1));color:#94a3b8;margin-top:-2px;margin-bottom:4px" title="' + escapeHtml(label) + '">' + escapeHtml(label) + '</div>';
	}

	function editUserMessage(btn) {
		var input = document.getElementById('messageInput');
		if (!input || !btn) return;
		input.value = decodeURIComponent(btn.getAttribute('data-message') || '');
		input.focus();
		var end = input.value.length;
		if (typeof input.setSelectionRange === 'function') {
			input.setSelectionRange(end, end);
		}
		input.dispatchEvent(new Event('input', { bubbles: true }));
	}
	window.editUserMessage = editUserMessage;

	function renderAsMarkdown(text) {
		try {
			return marked.parse(text);
		} catch (e) {
			return escapeHtml(text).replace(/\n/g, '<br>');
		}
	}

	function previewCollabFile(filePath) {
		var modal = document.getElementById('previewModal');
		var previewBody = document.getElementById('previewBody');
		var previewTitle = document.getElementById('previewTitle');
		modal.classList.remove('image-preview-active');
		// Normalize Windows backslashes to forward slashes for URL
		var normalizedPath = filePath.replace(/\\/g, '/');
		previewTitle.textContent = '预览: ' + (normalizedPath.split('/').pop() || '文件');
		previewBody.innerHTML = '<div style="text-align:center; padding:40px;">📄 加载中...</div>';
		modal.classList.add('active');

		// URL-encode each path segment (preserving / separators)
		var downloadUrl = buildFileDownloadUrl(normalizedPath);
		var ext = (normalizedPath.split('.').pop() || '').toLowerCase();

		fetch(downloadUrl)
			.then(function(resp) { return resp.text(); })
			.then(function(text) {
				if (ext === 'md' || ext === 'markdown') {
					previewBody.innerHTML = '<div class="markdown-preview" style="padding:20px;max-height:70vh;overflow-y:auto;">' + marked.parse(text) + '</div>';
				} else if (ext === 'json') {
					try {
						var parsed = JSON.parse(text);
						// Delivery file format: has content/title/source → render markdown
						if (parsed.content && typeof parsed.content === 'string') {
							var metaHtml = '';
							if (parsed.title) metaHtml += '<span style="margin-right:16px;">📝 ' + escapeHtml(parsed.title) + '</span>';
							if (parsed.source) metaHtml += '<span style="color:#888;">来源: ' + escapeHtml(parsed.source) + '</span>';
							previewBody.innerHTML =
								(metaHtml ? '<div style="padding:12px 16px;background:#f0f0f0;border-radius:8px 8px 0 0;font-size:calc(13.2px * var(--macs-font-scale, 1));border-bottom:1px solid #ddd;">' + metaHtml + '</div>' : '') +
								'<div class="markdown-preview" style="padding:20px;max-height:65vh;overflow-y:auto;">' + marked.parse(parsed.content) + '</div>';
						} else {
							text = JSON.stringify(parsed, null, 2);
							previewBody.innerHTML = '<pre style="padding:16px;background:#f6f8fa;border-radius:8px;overflow:auto;max-height:70vh;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.5;"><code>' + escapeHtml(text) + '</code></pre>';
						}
					} catch(e) {
						previewBody.innerHTML = '<pre style="padding:16px;background:#f6f8fa;border-radius:8px;overflow:auto;max-height:70vh;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.5;"><code>' + escapeHtml(text) + '</code></pre>';
					}
				} else {
					previewBody.innerHTML = '<pre style="padding:16px;background:#f6f8fa;border-radius:8px;overflow:auto;max-height:70vh;font-size:calc(14.3px * var(--macs-font-scale, 1));line-height:1.6;">' + escapeHtml(text) + '</pre>';
				}
			})
			.catch(function(err) {
				previewBody.innerHTML = '<div style="text-align:center;padding:40px;color:#dc3545;">❌ 加载失败: ' + escapeHtml(err.message) + '</div>';
			});
	}

	function renderCollabFallback(jsonStr) {
		// Try to parse partial JSON and extract readable info
		var text = jsonStr.replace(/[\x00-\x1f\x7f-\x9f]/g, '');
		try {
			var d = JSON.parse(text);
			if (d.collaboration_type || d.task_results) {
				var html = '<div style="background:#f3f4f6;border-radius:8px;padding:12px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;">';
				html += '🤝 协同结果';
				if (d.collaboration_type) html += ' · ' + escapeHtml(d.collaboration_type);
				if (d.successful_tasks != null) html += ' · ' + d.successful_tasks + '/' + (d.total_tasks || 0) + ' 成功';
				if (d.execution_duration) html += ' · ' + d.execution_duration.toFixed(1) + 's';
				if (d.meeting_minutes) {
					html += ' · <a href="' + buildFileDownloadUrl(d.meeting_minutes || '') + '" style="color:#2563eb;">📝 纪要</a>';
				}
				if (d.delivery_file) {
					html += ' · <a href="' + buildFileDownloadUrl(d.delivery_file || '') + '" style="color:#2563eb;">📦 交付物</a>';
				}
				html += '</div>';
				return html;
			}
		} catch(e) {}
		return null;
	}

	function renderCollaborationResult(jsonStr) {
		try {
			var data = JSON.parse(jsonStr);
			// Check if it's a collaboration result
			if (!data.collaboration_type && !data.task_results) return null;

			var html = '<div class="collab-result" style="background:#f8f9fa;border-radius:12px;padding:16px;font-size:calc(14.3px * var(--macs-font-scale, 1));">';

			// Header
			html += '<div style="font-weight:700;font-size:calc(16.5px * var(--macs-font-scale, 1));margin-bottom:12px;color:#333;">🤝 多Agent协同结果</div>';
			html += '<div style="display:flex;gap:16px;flex-wrap:wrap;margin-bottom:10px;">';
			html += '<span style="background:#e3f2fd;padding:4px 10px;border-radius:12px;font-size:calc(12.1px * var(--macs-font-scale, 1));">📋 ' + escapeHtml(data.collaboration_type || '') + '</span>';
			html += '<span style="background:#e8f5e9;padding:4px 10px;border-radius:12px;font-size:calc(12.1px * var(--macs-font-scale, 1));">✅ ' + (data.successful_tasks || 0) + '/' + (data.total_tasks || 0) + ' 成功</span>';
			if (data.execution_duration) {
				html += '<span style="background:#fff3e0;padding:4px 10px;border-radius:12px;font-size:calc(12.1px * var(--macs-font-scale, 1));">⏱ ' + data.execution_duration.toFixed(1) + 's</span>';
			}
			if (data.team_name) {
				html += '<span style="background:#f3e5f5;padding:4px 10px;border-radius:12px;font-size:calc(12.1px * var(--macs-font-scale, 1));">👥 团队: ' + escapeHtml(data.team_name) + '</span>';
			}
			if (data.task_description) {
				html += '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#666;margin-bottom:4px;">📝 任务: ' + escapeHtml(data.task_description) + '</div>';
			}
			html += '</div>';

			// Participating agents list with type info
			var collabModes = ['group_chat','sequential','parallel','vote','group_chat_with_host'];
			var results = data.task_results || [];
			// Collect unique agent names from task_results only
			var agentNames = [];
			var seenAgents = {};
			for (var i = 0; i < results.length; i++) {
				var an = (results[i].agent_name || '').trim();
				if (!an || seenAgents[an] || collabModes.indexOf(an) >= 0) continue;
				seenAgents[an] = true;
				agentNames.push(an);
			}
			// Also check for team members from the response
			if (agentNames.length === 0 && window._lastCollabAgents && window._lastCollabAgents.length > 0) {
				agentNames = window._lastCollabAgents.slice();
			}
			if (agentNames.length > 0) {
				html += '<div style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:12px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#666;">';
				html += '<span style="font-weight:600;">参与Agent: </span>';
				for (var i = 0; i < agentNames.length; i++) {
					var an = agentNames[i];
					var info = '';
					if (typeof agents !== 'undefined') {
						for (var j = 0; j < agents.length; j++) {
							if (agents[j].name === an) {
								// Try to extract role from system message
								var sm = agents[j].system_message || '';
								var roleMatch = sm.match(/你是[^\n。，]+/);
								if (!roleMatch) roleMatch = sm.match(/You are[^\n.,]+/i);
								if (!roleMatch) roleMatch = sm.match(/作为一名[^\n。，]+/);
								if (roleMatch) {
									info = roleMatch[0].replace(/^你是/, '').replace(/^You are /i, '').replace(/^作为一名/, '').trim();
									if (info.length > 24) info = info.slice(0, 24) + '…';
								} else {
									var t = agents[j].type || '';
									if (t === 'custom' || t === '') info = '';
									else if (t === 'assistant') info = 'AI助手';
									else if (t === 'user_proxy') info = '用户代理';
									else info = t;
								}
								break;
							}
						}
					}
					html += '<span style="background:#eef2ff;padding:2px 8px;border-radius:8px;" title="' + escapeHtml(an) + '">' +
						(info ? escapeHtml(info) + ' · ' : '') + escapeHtml(an) +
						'</span>';
				}
				html += '</div>';
			}

			// Helper to URL-encode path segments
		function encPath(p) {
			return (p || '').replace(/\\/g, '/').split('/').map(function(s) { return encodeURIComponent(s); }).join('/');
		}

		// Meeting minutes link with preview
		if (data.meeting_minutes) {
			var minPathRaw = (data.meeting_minutes || '').replace(/\\/g, '/');
			var minDownloadUrl = buildFileDownloadUrl(data.meeting_minutes);
			html += '<div style="display:flex;align-items:center;gap:8px;margin-bottom:12px;font-size:calc(13.2px * var(--macs-font-scale, 1));">' +
				'<span>📝 会议纪要</span>' +
				'<button onclick="window.open(\'' + minDownloadUrl + '\')" style="cursor:pointer;color:#1976D2;border:none;padding:3px 10px;background:#e3f2fd;border-radius:12px;font-size:calc(13.2px * var(--macs-font-scale, 1));">⬇ 下载</button>' +
				'<button onclick="previewCollabFile(\'' + escapeHtml(minPathRaw) + '\')" style="cursor:pointer;color:#10b981;border:none;padding:3px 10px;background:#e8f5e9;border-radius:12px;font-size:calc(13.2px * var(--macs-font-scale, 1));">👁 预览</button>' +
				'</div>';
		}

		// Delivery file link with preview
		if (data.delivery_file) {
			var delPathRaw = (data.delivery_file || '').replace(/\\/g, '/');
			var delDownloadUrl = buildFileDownloadUrl(data.delivery_file);
			html += '<div style="display:flex;align-items:center;gap:8px;margin-bottom:12px;font-size:calc(13.2px * var(--macs-font-scale, 1));">' +
				'<span>📦 交付物</span>' +
				'<button onclick="window.open(\'' + delDownloadUrl + '\')" style="cursor:pointer;color:#1976D2;border:none;padding:3px 10px;background:#e3f2fd;border-radius:12px;font-size:calc(13.2px * var(--macs-font-scale, 1));">⬇ 下载</button>' +
				'<button onclick="previewCollabFile(\'' + escapeHtml(delPathRaw) + '\')" style="cursor:pointer;color:#10b981;border:none;padding:3px 10px;background:#e8f5e9;border-radius:12px;font-size:calc(13.2px * var(--macs-font-scale, 1));">👁 预览</button>' +
				'</div>';
		}

			// Task results
			for (var i = 0; i < results.length; i++) {
				var r = results[i];
				// Look up agent role from system message
				var rtype = '';
				if (typeof agents !== 'undefined') {
					for (var j = 0; j < agents.length; j++) {
						if (agents[j].name === r.agent_name) {
							var sm = agents[j].system_message || '';
							var roleMatch = sm.match(/你是[^\n。，]+/);
							if (!roleMatch) roleMatch = sm.match(/You are[^\n.,]+/i);
							if (!roleMatch) roleMatch = sm.match(/作为一名[^\n。，]+/);
							if (roleMatch) {
								rtype = roleMatch[0].replace(/^你是/, '').replace(/^You are /i, '').replace(/^作为一名/, '').trim();
								if (rtype.length > 20) rtype = rtype.slice(0, 20) + '…';
							} else {
								var t = agents[j].type || '';
								if (t === 'custom' || t === '') rtype = '';
								else if (t === 'assistant') rtype = 'AI助手';
								else if (t === 'user_proxy') rtype = '用户代理';
								else rtype = t;
							}
							break;
						}
					}
				}
				html += '<div style="background:#fff;border:1px solid #e0e0e0;border-radius:8px;padding:12px;margin-bottom:8px;">';
				html += '<div style="font-weight:600;margin-bottom:2px;color:#555;">' +
					'🤖 ' + (rtype ? escapeHtml(rtype) + ' · ' : '') + escapeHtml(r.agent_name || 'Agent') +
					(r.status ? ' <span style="font-size:calc(11px * var(--macs-font-scale, 1));color:' + (r.status === 'failed' ? '#e53e3e' : '#38a169') + ';">· ' + escapeHtml(r.status) + '</span>' : '') +
					'</div>';
				var output = r.output || '';
				if (typeof output === 'string') {
					// Clean control characters that cause garbled text
					output = sanitizeAssistantBubbleContent(output.replace(/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/g, ''));
					html += '<div class="collab-agent-output" style="font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.7;color:#333;">' + renderAsMarkdown(output) + '</div>';
				} else {
					html += '<pre style="font-size:calc(12.1px * var(--macs-font-scale, 1));overflow-x:auto;background:#f5f5f5;padding:8px;border-radius:4px;">' + escapeHtml(sanitizeAssistantBubbleContent(JSON.stringify(output, null, 2))) + '</pre>';
				}
				html += '</div>';
			}

			html += '</div>';
			return html;
		} catch (e) {
			return null;
		}
	}

	function normalizeArtifactPathsForLinkify(text) {
		text = String(text || '');
		var toRelativeArtifactPath = function(raw) {
			var p = String(raw || '')
				.replace(/<br\s*\/?>/gi, '')
				.replace(/&nbsp;/gi, '')
				.replace(/\s+/g, '')
				.replace(/\\/g, '/')
				.replace(/[.,;:!?，。；：！？]+$/g, '');
			var lower = p.toLowerCase();
			var openKittyIdx = lower.lastIndexOf('/.openkitty/');
			if (openKittyIdx >= 0) return p.substring(openKittyIdx + 1);
			openKittyIdx = lower.indexOf('.openkitty/');
			if (openKittyIdx >= 0) return p.substring(openKittyIdx);
			return p;
		};
		var fullOpenKittyPathRe = /(?:[A-Za-z]:)?(?:[\\/][^\s<>"'`,;，。；]+)*[\\/]\.openkitty[\\/](?:\s|<br\s*\/?>|&nbsp;)*(?:storage|agent_deliveries|minutes)[\\/][^\s<>"'`,;，。；]+?\.[A-Za-z0-9]{1,8}/gi;
		text = text.replace(fullOpenKittyPathRe, function(match) {
			return toRelativeArtifactPath(match);
		});
		var relativeOpenKittyPathRe = /\.openkitty[\\/](?:\s|<br\s*\/?>|&nbsp;)*(?:storage|agent_deliveries|minutes)[\\/][^\s<>"'`,;，。；]+?\.[A-Za-z0-9]{1,8}/gi;
		text = text.replace(relativeOpenKittyPathRe, function(match) {
			return toRelativeArtifactPath(match);
		});
		return text;
	}

	function makePathsClickable(html) {
		html = normalizeArtifactPathsForLinkify(html);
		// First normalize backslashes to forward slashes
		html = html.replace(/(\.openkitty\/)?(storage|agent_deliveries|minutes)\\[^\s<>"',;]+\.\w+/g, function(m) { return m.replace(/\\/g, '/'); });
		var storageRe = /((?:\.openkitty\/)?storage\/[^\s<>"',;]+\.\w+)/g;
		var deliverRe = /((?:\.openkitty\/)?agent_deliveries\/[^\s<>"',;]+\.\w+)/g;
		var minutesRe = /((?:\.openkitty\/)?minutes\/[^\s<>"',;]+\.\w+)/g;
		var allRe = new RegExp('(' + [storageRe.source, deliverRe.source, minutesRe.source].join('|') + ')', 'g');

		var parts = []; var lastIdx = 0; var sm;
		while ((sm = allRe.exec(html)) !== null) {
			parts.push(html.substring(lastIdx, sm.index));
			var smatch = sm[1];
			var displayName = (smatch.replace(/\\/g, '/').split('/').pop() || '\u4e0b\u8f7d\u6587\u4ef6').replace(/&amp;/g, '&');
			if (/\.html?$/i.test(smatch)) {
				var sourceUrl = buildFileDownloadUrl(smatch);
				var previewUrl = buildFileViewUrl(smatch);
				parts.push('<span style="display:inline-flex;align-items:center;gap:4px;background:#f0f1ff;border-radius:6px;padding:2px 8px;font-size:calc(13.2px * var(--macs-font-scale, 1));">' +
					'<a href="' + sourceUrl + '" target="_blank" rel="noopener" style="color:#366ef4;text-decoration:underline;" title="下载 HTML 源文件">源码</a> | ' +
					'<a href="' + previewUrl + '" target="_blank" rel="noopener" style="color:#10b981;text-decoration:underline;" title="预览 HTML 页面">预览</a> ' +
					'<span style="color:#888;">' + escapeHtml(displayName) + '</span></span>');
			} else {
				var viewUrl = buildFileViewUrl(smatch);
				var dlUrl = buildFileDownloadUrl(smatch);
				parts.push('<span style="display:inline-flex;align-items:center;gap:4px;background:#f0f1ff;border-radius:6px;padding:2px 8px;font-size:calc(13.2px * var(--macs-font-scale, 1));">' +
					'<a href="' + viewUrl + '" target="_blank" style="color:#366ef4;text-decoration:underline;" title="预览">' + escapeHtml(displayName) + '</a>' +
					'<a href="' + dlUrl + '" style="color:#94a3b8;font-size:calc(11px * var(--macs-font-scale, 1));" title="下载">⬇</a></span>');
			}
			lastIdx = sm.index + smatch.length;
		}
		parts.push(html.substring(lastIdx));
		return parts.join('');
	}

	function attachTaskIdToLoading(loadingId, taskId) {
		var sessionId = '';
		if (typeof getTaskSession === 'function') sessionId = getTaskSession(taskId);
		if (!sessionId && typeof currentSessionId !== 'undefined') sessionId = currentSessionId || '';
		if (window.markTaskIdOnLoadingElement) {
			window.markTaskIdOnLoadingElement(loadingId, taskId, sessionId);
			return;
		}
		var loadingEl = document.getElementById(loadingId);
		if (!loadingEl || !taskId) return;
		loadingEl.setAttribute('data-task-id', taskId);
		if (sessionId) loadingEl.setAttribute('data-session-id', sessionId);
		var bubble = loadingEl.querySelector('.message-bubble');
		if (bubble) {
			bubble.setAttribute('data-task-id', taskId);
			if (sessionId) bubble.setAttribute('data-session-id', sessionId);
		}
		var btn = loadingEl.querySelector('.log-export-btn');
		if (btn) {
			btn.setAttribute('data-task-id', taskId);
			if (sessionId) btn.setAttribute('data-session-id', sessionId);
		}
	}
	window.attachTaskIdToLoading = attachTaskIdToLoading;

	function taskIdFromProgressLogHref(href) {
		href = (href || '').toString();
		var marker = '/api/task/progress-log/';
		var idx = href.indexOf(marker);
		if (idx < 0) return '';
		var tail = href.substring(idx + marker.length).split(/[?#]/)[0];
		try { return decodeURIComponent(tail); } catch(e) { return tail; }
	}

	function findTaskIdForProgressLogButton(btn) {
		var taskId = '';
		if (btn && btn.getAttribute) {
			taskId = btn.getAttribute('data-task-id') || '';
		}
		if (!taskId && btn && btn.closest) {
			var holder = btn.closest('[data-task-id]');
			if (holder) taskId = holder.getAttribute('data-task-id') || '';
		}
		if (!taskId && btn && btn.closest && window.pendingTasks) {
			var messageEl = btn.closest('.message');
			var loadingId = messageEl ? messageEl.id : '';
			if (loadingId) {
				for (var id in window.pendingTasks) {
					if (window.pendingTasks[id] === loadingId) {
						taskId = id;
						break;
					}
				}
			}
		}
		if (!taskId && btn && btn.closest) {
			var scope = btn.closest('.message-bubble') || btn.closest('.message') || document;
			var link = scope.querySelector && scope.querySelector('a[href*="/api/task/progress-log/"]');
			if (link) taskId = taskIdFromProgressLogHref(link.getAttribute('href') || link.href);
		}
		if (!taskId && window._currentTaskId) taskId = window._currentTaskId;
		if (!taskId && window._lastCompletedTaskId) taskId = window._lastCompletedTaskId;
		if (!taskId) {
			try { taskId = localStorage.getItem('lastCompletedTaskId') || ''; } catch(e) {}
		}
		return taskId;
	}

	function exportProgressLog(btn) {
		var taskId = findTaskIdForProgressLogButton(btn);
		if (!taskId) {
			alert('没有找到任务日志 ID');
			return;
		}
		var sessionId = '';
		if (btn && btn.getAttribute) sessionId = btn.getAttribute('data-session-id') || '';
		if (!sessionId && btn && btn.closest) {
			var sessionHolder = btn.closest('[data-session-id]');
			if (sessionHolder) sessionId = sessionHolder.getAttribute('data-session-id') || '';
		}
		if (!sessionId && typeof getTaskSession === 'function') sessionId = getTaskSession(taskId);
		if (!sessionId && typeof currentSessionId !== 'undefined') sessionId = currentSessionId || '';
		var resolvedTaskId = window.resolveProgressLogTaskId ? window.resolveProgressLogTaskId(taskId, sessionId) : taskId;
		var a = document.createElement('a');
		a.href = window.progressLogDownloadUrl ? window.progressLogDownloadUrl(taskId, sessionId) : '/api/task/progress-log/' + encodeURIComponent(resolvedTaskId);
		a.download = 'progress_' + resolvedTaskId + '.log';
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
	}
	window.exportProgressLog = exportProgressLog;

    async function createNewSession() {
		window.stopGenerationPolling?.();

		// 生成格式化时间：年后两位+月+日+时+分+秒

		const response = await fetch('/api/sessions', {
			method: 'POST',
			headers: {'Content-Type': 'application/json'},
			body: JSON.stringify({user_id: currentUserId || ''})
		});
		if (!response.ok) throw new Error('create session failed: HTTP ' + response.status);
		const result = await response.json();
		if (!result.session || !result.session.id) throw new Error('create session returned no server session id');

		// 最终格式：session_260327153025_xxxxxxx

		currentSessionId = result.session.id;

		currentSessionSource = 'web';

		// 保存到 localStorage

		if (typeof rememberActiveSessionId === 'function') {
			rememberActiveSessionId(currentSessionId, result.project && result.project.project_id);
		}

		setSessionHeaderTitle('新对话');

		document.getElementById('messagesArea').innerHTML = renderNewConversationState();
		if (typeof window.setCommunityConversationState === 'function') window.setCommunityConversationState(false);

			document.getElementById('welcomeTips') && (document.getElementById('welcomeTips').style.display = '');

		updateInputAreaState();  // 重新启用输入框

		loadSessions();

		addLog('info', '创建新会话: ' + currentSessionId);

	}

    async function sendMessage() {
        if (window.composerGenerationModel) { await window.sendGenerationMessage(); return; }
        var simpleQA = window.composerInteractionMode === 'chat';
        if (window.composerModelSelectionPending) {
            if (typeof showToast === 'function') showToast(chatText('composerModelApplying', '正在应用模型…'), 'info');
            return;
        }

		if (!isSessionEditable()) {

			alert('此会话来自其他渠道，仅可查看历史消息，不能发送新消息');

			return;

		}

		var input = document.getElementById('messageInput');

		var message = input.value.trim();

		var hasPendingAttachments = typeof window.hasPendingMessageAttachments === 'function' && window.hasPendingMessageAttachments();
		if (simpleQA && hasPendingAttachments) { showToast('发送附件请先切换到 Agent 模式', 'info'); return; }
		if (!message && !hasPendingAttachments) return;
		var selectedModelConfig = '';
		try {
			if (currentMode !== 'collab' && typeof window.resolveComposerModelSelection === 'function') selectedModelConfig = await window.resolveComposerModelSelection();
			if (typeof window.composerModelRequiresAccount === 'function' && window.composerModelRequiresAccount(selectedModelConfig)) {
				if (!await window.requirePlatformAccountLogin()) return;
			}
		} catch (modelError) {
			if (typeof showToast === 'function') showToast(modelError.message, 'error');
			return;
		}



		message = message

		.replace(/\u00A0/g, ' ')

		.replace(/\u200B/g, '')

		.replace(/\u3000/g, '  ')

		.replace(/&nbsp;/g, ' ')

		.replace(/&amp;/g, '&')

		.replace(/&lt;/g, '<')

		.replace(/&gt;/g, '>')

		.replace(/&quot;/g, '"')

		.replace(/&#39;/g, "'");

		var messageAttachments = [];
		if (typeof window.getPendingMessageAttachments === 'function') {
			try {
				messageAttachments = await window.getPendingMessageAttachments();
			} catch (attachmentError) {
				if (typeof showToast === 'function') showToast(attachmentError.message || '附件尚未上传完成', 'error');
				else alert(attachmentError.message || '附件尚未上传完成');
				return;
			}
		}
		var selectedSkillRun = typeof window.getSelectedSkillRunRequest === 'function'
			? window.getSelectedSkillRunRequest()
			: null;
		if (!simpleQA) await ensureSkillMentionListReady();
		var inlineSkillMention = !simpleQA && !selectedSkillRun && typeof window.messageHasInstalledSkillMention === 'function'
			? window.messageHasInstalledSkillMention(message)
			: false;

		if (currentMode === 'collab') {
			if (selectedSkillRun || inlineSkillMention) {
				showToast('Skill 运行只支持单 Agent；请先切换到单 Agent 模式', 'error');
				return;
			}
			if (messageAttachments.length > 0) {
				showToast('协同模式暂不接收附件，请切换到单 Agent 模式发送', 'error');
				return;
			}

			openCollabModal(message);

			input.value = '';

			return;

		}

		var unsentDraft = input.value;
		var unsentPromptState = typeof window.capturePromptComposerDraft === 'function' ? window.capturePromptComposerDraft() : null;

		// 如果 agent 正在执行，注入消息到运行中的任务，不创建新任务
		if (!currentSessionId) {
			await createNewSession();
			await new Promise(function(resolve) { setTimeout(resolve, 150); });
		}

		var activeTaskId = (typeof getActiveTaskForSession === 'function') ? getActiveTaskForSession(currentSessionId) : '';
		if (simpleQA && activeTaskId) { showToast('当前任务仍在运行，请等待完成后再发送问答', 'info'); return; }
		var userMsgRendered = false;
		if (activeTaskId && (selectedSkillRun || inlineSkillMention)) {
			showToast('当前任务仍在运行；Skill 会启动一个新任务，请等待当前任务结束后再发送', 'error');
			return;
		}
		if (activeTaskId && messageAttachments.length === 0) {
			var userMsg = {
				role: 'user',
				content: message,
				attachments: messageAttachments,
				created_at: new Date().toISOString(),
				agent_name: ''
			};
			var container = document.getElementById('messagesArea');
			container.innerHTML += renderMessage(userMsg);
			scrollToBottom();
	document.getElementById('welcomeTips') && (document.getElementById('welcomeTips').style.display = 'none');
			input.value = '';
			userMsgRendered = true;
			fetch('/api/interrupt', {
				method: 'POST',
				headers: {'Content-Type': 'application/json'},
				body: JSON.stringify({session_id: currentSessionId, task_id: activeTaskId, message: message})
			}).then(function(resp) {
				return resp.json().catch(function(){ return {}; }).then(function(data) {
				if (data.status === 'no_active_task' && typeof clearPendingTask === 'function') clearPendingTask(activeTaskId);
				if (resp.ok && data.status === 'injected') {
					addLog('info', '消息已注入到运行中的任务');
				} else {
					addLog('error', '消息注入失败');
				}
				});
			}).catch(function(err){
				addLog('error', '消息注入失败: ' + err.message);
			});
			return;
		}

		// 如果没有当前会话，创建新的

		if (!currentSessionId) {

			await createNewSession();

			await new Promise(function(resolve) { setTimeout(resolve, 150); });

		}

		var userMsg = {

			role: 'user',

			content: message,

			attachments: messageAttachments,

			created_at: new Date().toISOString(),

			agent_name: ''

		};

		var container = document.getElementById('messagesArea');

		clearConversationEmptyState(container);

		container.innerHTML += renderMessage(userMsg);
	document.getElementById('welcomeTips') && (document.getElementById('welcomeTips').style.display = 'none');

		scrollToBottom();

		input.value = '';

		isLoading = true;

		window._loadingStart = Date.now();

		window._lastProgress = Date.now();

		if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();

		// 创建 loading 消息，使用唯一 ID

		var loadingId = 'loading_' + Date.now() + '_' + Math.random().toString(36).substr(2, 8);

		var agentName = document.getElementById('agentSelect').value || "";
		// If message has @mention, use AE name as title
		var aeMatch = message.match(/@(\S+)/);
		var displayName = selectedSkillRun
			? selectedSkillRun.skill_name + ' · Skill'
			: (aeMatch ? aeMatch[1] : (agentName || 'AI助手'));

		var loadingHtml = '<div class="message assistant" id="' + loadingId + '">' +
				'<div class="message-bubble agent-run-card">' +
				'<div class="log-header">' +
					'<span class="log-title">' + escapeHtml(displayName) + '</span>' +
					'<span class="log-status" role="status" aria-live="polite"><span class="typing-dots" aria-hidden="true"><span class="typing-dot"></span><span class="typing-dot"></span><span class="typing-dot"></span></span><span class="loading-text">正在思考…</span></span>' +
				'</div>' +
				'<div class="loading-refresh-hint">等待时间较长，可能连接异常 <button class="loading-refresh-action" type="button" onclick="location.reload()">刷新</button></div>' +
				'<button class="log-summary" type="button" aria-expanded="false" hidden></button>' +
				'<div class="log-stream" role="log" aria-live="polite" aria-relevant="additions text"></div>' +
				'<button class="log-export-btn" title="下载日志" style="display:none;" onclick="exportProgressLog(this)">⬇ 下载日志</button>' +
				'</div>' +
				'</div>';
container.innerHTML += loadingHtml;

		scrollToBottom();

		addLog('info', '发送消息 (SessionId: ' + currentSessionId + '): ' + message.substring(0, 50));

		try {

			var agentName = document.getElementById('agentSelect').value;

			var requestData = {

				task_type: simpleQA ? 'qa' : selectedSkillRun ? 'skill' : 'chat',

				agent_name: simpleQA ? '' : agentName,

				message: message,

				attachments: messageAttachments,

				session_id: currentSessionId,

				channel: 'web',

				user_id: getUserId()

			};
			if (selectedModelConfig) requestData.model_config = selectedModelConfig;
			if (selectedSkillRun) requestData.skill_run = selectedSkillRun;

			var response = await fetch('/api/chat', {

				method: 'POST',

				headers: {

					'Content-Type': 'application/json; charset=utf-8'

				},

				body: JSON.stringify(requestData)

			});

			if (!response.ok) {
				var rejectedRequest = await response.json().catch(function () { return {}; });
				var rejectedMessage = rejectedRequest.error || ('HTTP ' + response.status);
				if (rejectedMessage === 'web_model_selection_unavailable') rejectedMessage = chatText('composerModelUnavailable', '请先选择可用模型，再发送消息。');
				if (rejectedMessage === 'web_model_selection_agent_unavailable') rejectedMessage = chatText('composerAgentUnavailable', '此 Agent 配置已改变，请重新选择助手后发送。');
				throw new Error(rejectedMessage);

			}



			var result = await response.json();
			if (typeof window.clearPendingMessageAttachments === 'function') {
				window.clearPendingMessageAttachments();
			}
			if ((result.task_id || result.result) && selectedSkillRun && typeof window.clearSelectedSkillRun === 'function') {
				window.clearSelectedSkillRun();
			}

			if (result.task_id) {

				// 保存任务 ID 和 loading ID 的映射

				if (typeof registerPendingTask === 'function') {
					registerPendingTask(result.task_id, loadingId, currentSessionId);
				} else {
					window.pendingTasks = window.pendingTasks || {};
					window.pendingTasks[result.task_id] = loadingId;
				}
				attachTaskIdToLoading(loadingId, result.task_id);

			// 按任务订阅专用 SSE
			subscribeTask(result.task_id, loadingId);

					window.pendingTaskTimes = window.pendingTaskTimes || {};

					window.pendingTaskTimes[result.task_id] = Date.now();

				addLog('info', '任务已创建: ' + result.task_id + ', loading ID: ' + loadingId);

				// 设置超时提示（60秒后如果还在等待）

				setTimeout(function() {

					var currentLoading = window.pendingTasks[result.task_id];

					if (currentLoading) {

						var loadingEl = document.getElementById(currentLoading);

						if (loadingEl && loadingEl.parentNode) {

							var textSpan = loadingEl.querySelector('.loading-text');

							if (textSpan) {

								textSpan.textContent = '处理时间较长，请稍候...';

								textSpan.style.color = '#856404';

							}

						}

					}

				}, 5000);

			} else if (result.result) {

				// 如果没有返回 task_id，直接显示结果

				var loadingEl = document.getElementById(loadingId);

				if (loadingEl) loadingEl.remove();

				var assistantMsg = {

					role: 'assistant',

					content: typeof result.result === 'string' ? result.result : JSON.stringify(result.result, null, 2),

					created_at: new Date().toISOString(),

					agent_name: agentName

				};

				container.innerHTML += renderMessage(assistantMsg);

				scrollToBottom();

				addLog('info', '收到响应');

				if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();

			}

			loadSessions();

		} catch (err) {
			if (!input.value) {
				input.value = unsentDraft;
				input.dispatchEvent(new Event('input', {bubbles: true}));
				if (typeof window.restorePromptComposerDraft === 'function') window.restorePromptComposerDraft(unsentPromptState);
			}

			console.error('发送失败:', err);

			addLog('error', '发送失败: ' + err.message);

			var loadingEl = document.getElementById(loadingId);

			if (loadingEl) loadingEl.remove();

			var errorMsg = {

				role: 'assistant',

				content: '❌ 发送失败: ' + err.message,

				created_at: new Date().toISOString(),

				agent_name: '系统'

			};

			container.innerHTML += renderMessage(errorMsg);

			scrollToBottom();

			if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();

		}

	}

    async function pollTaskResult(taskId, agentName) {

        for (var i = 0; i < 60; i++) {

            await new Promise(function(resolve) { setTimeout(resolve, 1000); });

            try {

                var response = await fetch('/api/task/' + taskId);

                var data = await response.json();

				if (data.status === 'completed' || data.status === 'degraded') {

					var resultContent = typeof data.result === 'string' ? data.result : JSON.stringify(data.result, null, 2);
					if (data.status === 'degraded') {
						resultContent = '⚠️ 降级完成：部分能力不可用或结果存在限制。\n\n' + (resultContent || '未返回可用结果。');
						addLog('warning', '任务 ' + taskId + ' 降级完成（结果可能不完整）');
					}

					var assistantMsg = {

                        role: 'assistant',

						content: resultContent,

                        created_at: new Date().toISOString(),

                        agent_name: agentName,

                        task_id: taskId

                    };

                    var container = document.getElementById('messagesArea');

                    container.innerHTML += renderMessage(assistantMsg);

                    scrollToBottom();

                    break;

                } else if (data.status === 'failed' || data.status === 'stopped') {
					data.agent_name = data.agent_name || agentName;
					if (typeof window.renderFailedTaskResult === 'function') {
						window.renderFailedTaskResult(data, null);
					}

					addLog(data.status === 'stopped' ? 'info' : 'error', data.status === 'stopped'
						? '任务已停止'
						: '任务失败: ' + (data.error || '未知错误'));

                    break;

                }

            } catch (err) {

                console.error('轮询失败:', err);

            }

        }

    }

    function openCollabModal(message) {

		document.getElementById('collabMessage').value = message || '';

		document.getElementById('collabProgress').style.display = 'none';

		// Refresh agent list and host selector
		loadAgents().then(function() {
			var collabList = document.getElementById('collabAgentsList');
			var collabHtml = '';
			var hostSelect = document.getElementById('hostAgent');
			hostSelect.innerHTML = '<option value="">请选择主持人</option>';
			for (var i = 0; i < agents.length; i++) {
				collabHtml += '<div class="agent-checkbox">' +
					'<input type="checkbox" value="' + agents[i].name + '" id="collab_agent_' + i + '" checked>' +
					'<label for="collab_agent_' + i + '">' + agents[i].name + ' (' + agents[i].type + ')</label>' +
					'</div>';
				hostSelect.innerHTML += '<option value="' + agents[i].name + '">' + agents[i].name + '</option>';
			}
			collabList.innerHTML = collabHtml;
		});

		document.getElementById('collabModal').classList.add('active');

	}

    function closeCollabModal() {

        document.getElementById('collabModal').classList.remove('active');

    }

    async function startCollaboration() {

		var mode = document.querySelector('input[name="collabMode"]:checked').value;

		var selectedAgents = [];
		window._lastCollabAgents = [];

		var checkboxes = document.querySelectorAll('#collabAgentsList input:checked');

		for (var i = 0; i < checkboxes.length; i++) {

			selectedAgents.push(checkboxes[i].value);

		}

		if (selectedAgents.length === 0) {

			alert('请至少选择一个Agent');

			return;

		}
		window._lastCollabAgents = selectedAgents.slice();

		var message = document.getElementById('collabMessage').value;

		if (!message.trim()) {

			alert('请输入讨论主题或任务');

			return;

		}

		var maxRounds = parseInt(document.getElementById('maxRounds').value);

		var failureStrategy = document.getElementById('failureStrategy').value;

		var hostAgent = document.getElementById('hostAgent').value;

		// 如果是主持讨论模式但没有选择主持人

		if (mode === 'group_chat_with_host' && !hostAgent) {

			alert('请选择主持人');

			return;

		}

		// 构建任务配置

		var tasks = [];

		for (var i = 0; i < selectedAgents.length; i++) {

			tasks.push({

				task_id: 'task_' + (i + 1),

				agent: selectedAgents[i]

			});

		}

		var collabConfig = {

			type: mode,

			tasks: tasks,

			max_round: maxRounds,

			failure_strategy: failureStrategy

		};

		// 如果是主持讨论模式，添加主持人配置

		if (mode === 'group_chat_with_host') {

			collabConfig.host_agent = hostAgent;

			// 分组配置

			collabConfig.groups = [];

			var groupSize = Math.ceil(selectedAgents.length / 3);

			for (var i = 0; i < selectedAgents.length; i += groupSize) {

				collabConfig.groups.push({

					name: 'group_' + (Math.floor(i / groupSize) + 1),

					agents: selectedAgents.slice(i, i + groupSize)

				});

			}

		}

		// Ensure we have a session
		if (!currentSessionId) {
			await createNewSession();
			await new Promise(function(resolve) { setTimeout(resolve, 150); });
		}

		var requestData = {

			task_type: 'collaboration',

			message: message,

			session_id: currentSessionId,

			collaboration: collabConfig,

			parameters: {

				team_name: '协同团队' + (function(){var d=new Date();return ''+d.getFullYear()+('0'+(d.getMonth()+1)).slice(-2)+('0'+d.getDate()).slice(-2)+('0'+d.getHours()).slice(-2)+('0'+d.getMinutes()).slice(-2)+('0'+d.getSeconds()).slice(-2)})()

			}

		};

		document.getElementById('collabProgress').style.display = 'block';

		document.getElementById('progressFill').style.width = '0%';

		document.getElementById('collabStatus').innerHTML = '🚀 准备开始...';

		closeCollabModal();

		// Add user message and loading bubble to chat area
		var container = document.getElementById('messagesArea');
		clearConversationEmptyState(container);
		var collabUserMsg = {
			role: 'user',
			content: '🤝 [多Agent协同] ' + message,
			created_at: new Date().toISOString(),
			agent_name: ''
		};
		container.innerHTML += renderMessage(collabUserMsg);

		// Create loading bubble
		var loadingId = 'loading_' + Date.now() + '_' + Math.random().toString(36).substr(2, 8);
		var collabAgentNames = selectedAgents.join(', ');
		var loadingHtml = '<div class="message assistant" id="' + loadingId + '">' +
			'<div class="message-bubble agent-run-card">' +
			'<div class="log-header"><span class="log-title">🤝 协同团队 <small>(' + escapeHtml(getModeName(mode) + ': ' + collabAgentNames) + ')</small></span>' +
			'<span class="log-status" role="status" aria-live="polite"><span class="typing-dots" aria-hidden="true"><span class="typing-dot"></span><span class="typing-dot"></span><span class="typing-dot"></span></span><span class="loading-text">协同任务准备中…</span></span></div>' +
			'<div class="loading-refresh-hint">等待时间较长，可能连接异常 <button class="loading-refresh-action" type="button" onclick="location.reload()">刷新</button></div>' +
			'<button class="log-summary" type="button" aria-expanded="false" hidden></button>' +
			'<div class="log-stream" role="log" aria-live="polite" aria-relevant="additions text"></div>' +
			'<button class="log-export-btn" title="下载日志" style="display:none;" onclick="exportProgressLog(this)">⬇ 下载日志</button>' +
			'</div>' +
			'</div>';
		container.innerHTML += loadingHtml;
		scrollToBottom();

		addLog('info', '开始协同任务');
		addLog('info', '模式: ' + getModeName(mode));
		addLog('info', '参与Agent: ' + selectedAgents.join(', '));
		addLog('info', '最大轮次: ' + maxRounds);
		addLog('info', '失败策略: ' + getStrategyName(failureStrategy));

		if (mode === 'group_chat_with_host') {

			addLog('info', '主持人: ' + hostAgent);

		}

		try {

			var response = await fetch('/api/collaboration', {

				method: 'POST',

				headers: {

					'Content-Type': 'application/json; charset=utf-8'

				},

				body: JSON.stringify(requestData)

			});

			var result = await response.json();

			if (result.task_id) {

				currentCollabTaskId = result.task_id;

				// Register for SSE updates
				if (typeof registerPendingTask === 'function') {
					registerPendingTask(result.task_id, loadingId, currentSessionId);
				} else {
					window.pendingTasks = window.pendingTasks || {};
					window.pendingTasks[result.task_id] = loadingId;
				}
				attachTaskIdToLoading(loadingId, result.task_id);
				window.pendingTaskTimes = window.pendingTaskTimes || {};
				window.pendingTaskTimes[result.task_id] = Date.now();

				pollCollaborationProgress(result.task_id, loadingId);

			} else {
				// No task_id means immediate error, remove loading
				var loadingEl = document.getElementById(loadingId);
				if (loadingEl) loadingEl.remove();
			}

		} catch (err) {

			console.error('启动协同失败:', err);

			addLog('error', '启动协同失败: ' + err.message);

			document.getElementById('collabStatus').innerHTML = '❌ 启动失败: ' + err.message;

			var loadingEl = document.getElementById(loadingId);
			if (loadingEl) loadingEl.remove();
			alert('启动协同失败: ' + err.message);

		}

	}

    async function pollCollaborationProgress(taskId, initialLoadingId) {

		var completed = false;

		var attempts = 0;

		var maxAttempts = 300;

		while (!completed && attempts < maxAttempts) {

			await new Promise(function(resolve) { setTimeout(resolve, 1000); });

			attempts++;

			try {

				var response = await fetch('/api/task/' + taskId);

				var data = await response.json();

				var statusHtml = '';

				statusHtml += '📊 状态: ' + data.status + '<br>';

				statusHtml += '⏱️ 耗时: ' + attempts + '秒<br>';

				if (data.progress) {

					var percent = (data.progress.completed / data.progress.total * 100);

					document.getElementById('progressFill').style.width = percent + '%';

					statusHtml += '📈 进度: ' + data.progress.completed + '/' + data.progress.total + '<br>';

					if (data.progress.current) {

						statusHtml += '🔨 当前任务: ' + data.progress.current + '<br>';

					}

				}

				if (data.result && data.result.responses) {

					statusHtml += '<br>📝 响应详情:<br>';

					// Update the chat loading bubble with intermediate agent responses
					var activeLoadingId = (window.pendingTasks && window.pendingTasks[taskId]) || initialLoadingId;
					var loadingEl = activeLoadingId ? document.getElementById(activeLoadingId) : null;
					if (loadingEl) {
						var bubble = loadingEl.querySelector('.message-bubble');
						if (bubble) {
							var responsesHtml = '';
							for (var i = 0; i < data.result.responses.length; i++) {
								var resp = data.result.responses[i];
								var safeResponse = sanitizeAssistantBubbleContent(resp.response || '');
								var respText = safeResponse.substring(0, 200);
								responsesHtml += '<div style="margin-top:6px;padding:6px 8px;background:#f0f4ff;border-radius:6px;font-size:calc(12.1px * var(--macs-font-scale, 1));">' +
									'<span style="font-weight:600;">🤖 ' + escapeHtml(resp.agent || 'Agent') + '</span>' +
									'<span style="color:#38a169;margin-left:6px;">✓</span>' +
									'<div style="color:#555;margin-top:3px;line-height:1.5;">' + escapeHtml(respText) + (safeResponse.length > 200 ? '...' : '') + '</div>' +
									'</div>';
								statusHtml += '  • ' + escapeHtml(resp.agent || 'Agent') + ': ' + escapeHtml(safeResponse.substring(0, 100)) + '...<br>';
							}
							// Replace loading content with live responses
							var agentNameEl = bubble.querySelector('.agent-name');
							var existingResponses = bubble.querySelector('.live-responses');
							if (existingResponses) {
								existingResponses.innerHTML = responsesHtml;
							} else {
								var div = document.createElement('div');
								div.className = 'live-responses';
								div.innerHTML = responsesHtml;
								bubble.appendChild(div);
								if (agentNameEl) {
									var hintEl = agentNameEl.querySelector('.loading-text');
									if (hintEl) hintEl.textContent = '协同进行中...';
								}
							}
						}
					}

				}

				document.getElementById('collabStatus').innerHTML = statusHtml;

				if (data.status === 'completed' || data.status === 'degraded') {

					completed = true;
					var collaborationDegraded = data.status === 'degraded';

					document.getElementById('progressFill').style.width = '100%';

					document.getElementById('collabStatus').innerHTML += collaborationDegraded
						? '<br>⚠️ 协同降级完成，结果可能不完整。'
						: '<br>✅ 协同完成！';

					addLog(collaborationDegraded ? 'warning' : 'info',
						(collaborationDegraded ? '协同任务降级完成: ' : '协同任务完成: ') + taskId);

					// If SSE already removed the loading bubble, refresh session instead of adding duplicate
					var activeLoadingId = (window.pendingTasks && window.pendingTasks[taskId]) || initialLoadingId;
					var loadingEl = activeLoadingId ? document.getElementById(activeLoadingId) : null;
					if (!loadingEl) {
						// SSE already handled it, just refresh session
						if (currentSessionId) {
							try { await loadSession(currentSessionId); } catch(e) {}
						}
					} else {
						// SSE hasn't handled yet, show result
						var msgBubble = loadingEl.closest('.message-bubble');
						loadingEl.remove();
						if (typeof clearPendingTask === 'function') clearPendingTask(taskId);
						else delete window.pendingTasks[taskId];
						var resultContent = (collaborationDegraded
							? '⚠️ 协同任务降级完成：部分结果可能不可用。'
							: '🎉 协同任务完成！') + '\n\n📋 结果摘要:\n' + formatCollaborationResult(data.result);
						var resultMsg = {
							role: 'assistant',
							content: resultContent,
							created_at: new Date().toISOString(),
							agent_name: '协同系统',
							task_id: taskId
						};
						var container = document.getElementById('messagesArea');
						clearConversationEmptyState(container);
						container.innerHTML += renderMessage(resultMsg);
						scrollToBottom();
					}

					setTimeout(function() {
						document.getElementById('collabProgress').style.display = 'none';
					}, 5000);

					break;

				} else if (data.status === 'failed' || data.status === 'stopped') {

					completed = true;

					document.getElementById('collabStatus').innerHTML += data.status === 'stopped'
						? '<br>⏹️ 协同任务已停止'
						: '<br>处理未完成，请重试。';

					addLog(data.status === 'stopped' ? 'info' : 'error', data.status === 'stopped'
						? '协同任务已停止'
						: '协同失败: ' + (data.error || '未知错误'));

					var loadingId2 = (window.pendingTasks && window.pendingTasks[taskId]) || initialLoadingId;
					data.agent_name = data.agent_name || '协同系统';
					if (typeof window.renderFailedTaskResult === 'function') {
						window.renderFailedTaskResult(data, loadingId2);
					}

					break;

				}

			} catch (err) {

				console.error('轮询失败:', err);

			}

		}

	}

    function stopCollaboration() {

        if (currentCollabTaskId) {

            fetch('/api/task/stop', {

                method: 'POST',

                headers: { 'Content-Type': 'application/json' },

                body: JSON.stringify({ task_id: currentCollabTaskId })

            }).then(function() {

                document.getElementById('collabStatus').textContent = '已停止';

                addLog('info', '停止协同任务: ' + currentCollabTaskId);

            }).catch(function(err) {

                console.error('停止失败:', err);

            });

        }

    }

	function formatCollaborationResult(result) {

		if (!result) return '无结果';

		var output = '';

		if (result.summary) {

			output += '总结: ' + result.summary + '\n\n';

		}

		if (result.vote_result) {

			output += '投票结果: ' + JSON.stringify(result.vote_result, null, 2) + '\n\n';

		}

		if (result.final_answer) {

			output += '最终答案: ' + result.final_answer + '\n\n';

		}

		if (result.responses) {

			output += '各Agent响应:\n';

			for (var i = 0; i < result.responses.length; i++) {

				var resp = result.responses[i];

				output += '  ' + resp.agent + ': ' + resp.response + '\n';

			}

		}

		return output || JSON.stringify(result, null, 2);

	}

	function getModeName(mode) {

		var modes = {

			'group_chat': '群组讨论',

			'sequential': '顺序执行',

			'parallel': '并行执行',

			'vote': '投票决策',

			'group_chat_with_host': '有主持讨论'

		};

		return modes[mode] || mode;

	}

	function getStrategyName(strategy) {

		var strategies = {

			'stop': '遇到失败停止',

			'continue': '继续执行',

			'retry': '重试失败任务'

		};

		return strategies[strategy] || strategy;

	}

	async function exportToPdf() {
		var title = redactAccountInfoForExport(document.getElementById('sessionTitle').textContent || 'AI对话记录');
		var messages = Array.from(document.querySelectorAll('#messagesArea .message')).filter(function(message){return message.id !== 'syncingIndicator';});
		if (!messages.length) { alert('当前没有对话内容可导出'); return; }
		var w=window.open('', '_blank', 'width=900,height=700');
		if(!w){showToast(chatText('exportPopupBlocked','请允许打开导出窗口。'),'info');return;}
		w.document.write('<!DOCTYPE html><meta charset="UTF-8"><title>macs</title><p style="font:14px sans-serif;padding:24px">'+escapeHtml(chatText('exportPreparing','正在准备导出图片…'))+'</p>');w.document.close();
		var snapshots=[], musicStyles="";
		try {
			for(var message of messages){if(w.closed)return;snapshots.push(await window.prepareConversationExportMessage(message));}
			if(messages.some(function(message){return message.querySelector(".generation-music-card");})){
				var styles=await fetch("/web/music_player.css",{credentials:"same-origin",signal:AbortSignal.timeout(10000)});
				if(!styles.ok)throw new Error("Unable to load music styles");musicStyles=await styles.text();
			}
		} catch(error) {
			if(!w.closed){w.document.body.textContent=error.message;w.document.body.style.cssText='font:14px sans-serif;padding:24px;';}
			showToast(error.message,'error');return;
		}
		if(w.closed)return;

		var html = '<!DOCTYPE html><html><head><meta charset="UTF-8"><title>' + escapeHtml(title) + '</title><style>' +
			'*{margin:0;padding:0;box-sizing:border-box}' +
			'.ico{width:16px;height:16px;display:inline-block;vertical-align:middle;flex-shrink:0}' +
			'.ico svg{width:100%;height:100%;stroke:currentColor;fill:none;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}' +
			'body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"Microsoft YaHei","PingFang SC",sans-serif;font-size:14px;color:#1e293b;line-height:1.5;margin:0 auto;max-width:900px;background:#fafaf9}' +
			/* Page header */
			'.page-header{background:#FBFBFA;border-bottom:1px solid #EAEAEA;padding:12px 32px 10px;margin:0}' +
			'.page-header h1{font-size:20px;color:#1e293b;font-weight:700;letter-spacing:0.02em}' +
			'.subtitle{font-size:11px;color:#64748b;margin-top:4px}' +
			/* Content area */
			'.content{padding:10px 32px 24px}' +
			/* Messages */
			'.row{display:flex;flex-direction:column;margin-bottom:14px;break-inside:avoid;page-break-inside:avoid}' +
			'.row.user{align-items:flex-end} .row.assistant{align-items:flex-start}' +
			'.msg{max-width:85%;min-width:15%;padding:12px 18px;border-radius:18px;overflow-wrap:break-word;word-break:break-word;line-height:1.6}' +
			'.msg.user{background:#DBEAFE;color:#1E3A5F;border-bottom-right-radius:4px;box-shadow:0 1px 3px rgba(0,0,0,0.08)}' +
			'.msg.assistant{background:#f5f2ee;color:#1a1a2e;border:1px solid #e0dbd3;border-bottom-left-radius:4px}' +
			'.msg .name{font-size:12px;font-weight:600;margin-bottom:6px}' +
			'.row.user .msg .name{color:#1E3A5F}' +
			'.row.assistant .msg .name{color:#1F6C9F}' +
			'.msg .time{font-size:10px;color:#64748b;margin-top:6px}' +
			'.row.assistant .msg .time{color:#94a3b8}' +
			'.msg .body{font-size:14px;line-height:1.5}' +
			/* Markdown elements inside messages */
			'.msg .body h1,.msg .body h2,.msg .body h3,.msg .body h4,.msg .body h5,.msg .body h6{margin:6px 0 3px;color:inherit;font-weight:600}' +
			'.msg .body h1{font-size:1.3em;border-bottom:1px solid #e2e8f0;padding-bottom:4px}' +
			'.msg .body h2{font-size:1.15em}' +
			'.msg .body h3{font-size:1.05em}' +
			'.msg .body p{margin:3px 0}' +
			'.msg .body ul,.msg .body ol{padding-left:20px;margin:3px 0}' +
			'.msg .body li{margin:1px 0}' +
			'.msg .body a{color:#3b82f6;text-decoration:underline}' +
			'.msg .body strong{font-weight:600}' +
			'.msg .body hr{border:none;border-top:1px solid #e2e8f0;margin:6px 0}' +
			'.row.user .msg .body h1,.row.user .msg .body h2,.row.user .msg .body h3{color:#fff;border-bottom-color:rgba(255,255,255,0.2)}' +
			'.row.user .msg .body a{color:#93c5fd}' +
			'.row.user .msg .body hr{border-top-color:rgba(255,255,255,0.15)}' +
			'img{max-width:100%;border-radius:4px;margin:6px 0}' +
			'.generation-result-image{display:block;width:auto;height:auto;max-height:none}' +
			'.generation-result-status{display:block;margin-bottom:8px;font-weight:600}' +
			'.generation-result-billing{margin-top:8px;font-size:12px;color:#64748b}' +
			'.generation-export-media{padding:16px;border:1px solid #dbe3ef;border-radius:10px;background:#f8fafc;margin:12px 0}' +
			'.generation-export-media strong,.generation-export-media a{display:block}.generation-export-media a{margin-top:8px}' +
			'.msg.music-result{width:85%}.generation-export-lyrics{margin:12px 0}.generation-export-lyrics pre{background:#f8f9fc;color:#475569;border:1px solid #e2e8f0;line-height:1.8}' +
			'audio,video{display:block;width:100%;margin:10px 0}' +
			'body.img-mode audio,body.img-mode video{display:none}' +
			/* Code */
			'pre{background:#1e293b;color:#cbd5e1;padding:14px 18px;border-radius:6px;overflow-x:auto;font-size:12px;line-height:1.6;margin:8px 0;page-break-inside:avoid;white-space:pre-wrap;word-break:break-all}' +
			'code{background:#eff6ff;color:#1d4ed8;padding:1px 5px;border-radius:3px;font-size:0.9em;font-family:"JetBrains Mono","Fira Code","Cascadia Code",monospace}' +
			'pre code{background:transparent;color:inherit;padding:0;font-size:inherit;white-space:pre-wrap}' +
			/* Tables */
			'table{border-collapse:collapse;width:100%;margin:10px 0;font-size:12px;page-break-inside:avoid}' +
			'th,td{border:1px solid #d1d5db;padding:8px 12px;text-align:left;vertical-align:top}' +
			'th{background:#eff6ff;color:#374151;font-weight:600;font-size:11px;text-transform:uppercase;letter-spacing:0.03em}' +
			'td{color:#374151}' +
			'tr:nth-child(even) td{background:#fafbfc}' +
			/* Blockquote */
			'blockquote{border-left:3px solid #64748b;padding:6px 14px;margin:8px 0;background:#f8fafc;color:#2563eb}' +
			'.row.user blockquote{border-left-color:rgba(255,255,255,0.3);background:rgba(255,255,255,0.08);color:rgba(255,255,255,0.8)}' +
			/* Buttons */
			'.print-bar{display:flex;justify-content:flex-end;gap:10px;padding:14px 32px;background:#f8fafc;border-bottom:1px solid #e2e8f0}' +
			'.print-bar button{padding:8px 20px;border:1px solid #d1d5db;border-radius:6px;font-size:12px;cursor:pointer;font-weight:500;background:#fff;color:#374151;transition:all 0.15s}' +
			'.print-bar button:hover{background:#eff6ff;border-color:#94a3b8}' +
			/* Watermark */
			'.watermark{text-align:center;font-size:8px;color:#cbd5e1;padding:16px 0 0;margin:24px 32px 12px;border-top:1px solid #e2e8f0;letter-spacing:0.02em}' +
			/* Image mode (html2canvas) */
			'body.img-mode{background:#fafaf9}' +
			'body.img-mode .msg{max-width:85%!important;min-width:10%!important}' +
			'body.img-mode .row.user .msg{margin-left:auto!important}' +
			'body.img-mode .row.assistant .msg{margin-right:auto!important}' +
			'body.img-mode .print-bar{display:none}' +
			/* Print */
			'@media print{' +
			'@page{size:A4;margin:15mm 12mm}' +
			'*{-webkit-print-color-adjust:exact!important;print-color-adjust:exact!important;box-shadow:none!important;text-shadow:none!important}' +
			'body{font-size:12px!important;line-height:1.45!important;color:#000!important;background:#FBFBFA!important;max-width:100%!important}' +
			'.page-header{padding:8px 24px 6px!important;background:#FBFBFA!important;-webkit-print-color-adjust:exact!important;print-color-adjust:exact!important}' +
			'.page-header h1{font-size:15px!important;color:#1e293b!important}' +
			'.content{padding:6px 24px 14px!important}' +
			'.watermark{display:none}.print-bar{display:none}audio,video{display:none}' +
			'.row{display:block!important;page-break-inside:avoid!important;break-inside:avoid!important;margin-bottom:12px!important;overflow:visible!important}' +
			'.row.user{text-align:right!important}.row.assistant{text-align:left!important}' +
			'.msg{display:inline-block!important;max-width:85%!important;text-align:left!important;padding:8px 14px!important;margin:0!important;border-radius:0!important;overflow:visible!important;word-break:normal!important}' +
			'.msg.user{background:#DBEAFE!important;color:#1E3A5F!important}' +
			'.msg.assistant{background:#f5f2ee!important;color:#1a1a2e!important;border:1px solid #d5d0c8!important}' +
			'.msg .name{font-size:11px!important;font-weight:600!important;margin-bottom:4px!important}' +
			'.msg .body{font-size:12px!important;line-height:1.45!important;overflow:visible!important}' +
			'.msg .body h1{font-size:1.15em!important;margin:4px 0 2px!important}' +
			'.msg .body h2{font-size:1.08em!important;margin:4px 0 2px!important}' +
			'.msg .body h3{font-size:1.03em!important;margin:4px 0 2px!important}' +
			'.msg .body p,.msg .body ul,.msg .body ol{margin:2px 0!important}' +
			'pre{font-size:10px!important;padding:6px 10px!important;margin:4px 0!important;white-space:pre-wrap!important;overflow:visible!important}' +
			'table{font-size:10px!important}th,td{padding:4px 8px!important}' +
			'img{max-width:100%!important}' +
			'}' +
			musicStyles + '</style></head><body>' +
			'<div class="print-bar">' +
			'<button onclick="window.print()">导出 PDF</button>' +
			'<button class="btn-img">导出图片</button>' +
			'</div>' +
			'<div class="page-header">' +
			'<h1>' + escapeHtml(title) + '</h1>' +
			'<div class="subtitle">' + formatDate(new Date()) + ' &middot; macs</div>' +
			'</div>' +
			'<div class="content">';

		snapshots.forEach(function(snapshot) {
			var role=snapshot.role,content=snapshot.content,name=snapshot.name,time=snapshot.time;
			if (!content || content.length < 3) return;
				html += '<div class="row ' + role + '"><div class="msg ' + role + (snapshot.music?' music-result':'') + '">' +
					'<div class="name">' + escapeHtml(name) + '</div>' +
					'<div class="body">' + content + '</div>' +
					'<div class="time">' + escapeHtml(time) + '</div>' +
					'</div></div>';
			});

	html += '</div>'; // close .content
	html += '<div class="watermark">macs | 内容由 AI 生成，仅供参考，不构成任何建议</div>';
		html += '<script>window.addEventListener("DOMContentLoaded",function(){window.opener.installConversationExportActions(window);});<\/script></body></html>';

		var exportURL=URL.createObjectURL(new Blob([html],{type:'text/html;charset=utf-8'}));
		w.location.replace(exportURL);
		setTimeout(function(){URL.revokeObjectURL(exportURL);},60000);
		// No auto-print; user clicks buttons at top
	}


// encodePath encodes each path segment separately, preserving /
function encodePath(p) {
	return p.split('/').map(function(s) { return encodeURIComponent(s); }).join('/');
}

function normalizeFilePathForAPI(p) {
	p = String(p || '').trim().replace(/\\/g, '/');
	while (p.indexOf('//') >= 0) p = p.replace(/\/\//g, '/');
	if (p.indexOf('/.openkitty/') === 0 || p.indexOf('/storage/') === 0 || p.indexOf('/agent_deliveries/') === 0 || p.indexOf('/minutes/') === 0) {
		p = p.substring(1);
	}
	return p;
}

function buildFileDownloadUrl(p) {
	return '/api/files/download?path=' + encodeURIComponent(normalizeFilePathForAPI(p));
}

function buildFileInlineUrl(p) {
	return '/api/files/download?inline=1&path=' + encodeURIComponent(normalizeFilePathForAPI(p));
}

function buildFileViewUrl(p) {
	return '/web/file-view.html?path=' + encodeURIComponent(normalizeFilePathForAPI(p));
}
