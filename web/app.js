    // PDF.js 配置

    let isLoading = false;

    // Guard against CDN failure -- pdf.js may not load in air-gapped or China deployments
    if (typeof pdfjsLib !== 'undefined') {
        pdfjsLib.GlobalWorkerOptions.workerSrc = '/web/pdf.worker.min.js';
    }

    let currentSessionId = null;
	let currentProjectId = '';

	function activeSessionStorageKey(projectId) {
		return 'currentSessionId:' + String(projectId || 'unknown');
	}

	function rememberActiveSessionId(sessionId, projectId) {
		if (projectId) currentProjectId = String(projectId);
		try {
			localStorage.removeItem('currentSessionId');
			if (!currentProjectId) return;
			if (sessionId) sessionStorage.setItem(activeSessionStorageKey(currentProjectId), sessionId);
			else sessionStorage.removeItem(activeSessionStorageKey(currentProjectId));
		} catch (e) {}
	}
	window.rememberActiveSessionId = rememberActiveSessionId;

	let currentUserId = null; // 可选，可以从登录信息获取

    let agents = [];

    // Keep one preference rule for every Agent/model selector. An explicit
    // default_agent wins, otherwise Assistant/Assist is preferred, followed by
    // the remaining names in a stable alphabetical order.
    function agentPreferenceRank(name, preferredName) {
        var normalized = String(name || '').trim().toLowerCase();
        var preferred = String(preferredName || '').trim().toLowerCase();
        if (preferred && normalized === preferred) return -1;
        if (normalized === 'assistant') return 0;
        if (normalized === 'assist') return 1;
        if (normalized.indexOf('assist') === 0) return 2;
        return 3;
    }

    function prioritizedAgentEntries(agentList, preferredName) {
        return (agentList || []).map(function(agent, index) {
            return { agent: agent || {}, index: index };
        }).sort(function(a, b) {
            var rankA = agentPreferenceRank(a.agent.name, preferredName);
            var rankB = agentPreferenceRank(b.agent.name, preferredName);
            if (rankA !== rankB) return rankA - rankB;
            var nameA = String(a.agent.name || '').toLowerCase();
            var nameB = String(b.agent.name || '').toLowerCase();
            var compared = nameA.localeCompare(nameB);
            return compared || (a.index - b.index);
        });
    }

    function sortAgentsAssistantFirst(agentList, preferredName) {
        return prioritizedAgentEntries(agentList, preferredName).map(function(entry) {
            return entry.agent;
        });
    }

    function preferredAssistantAgent(config) {
        config = config || {};
        var sorted = sortAgentsAssistantFirst(config.agents || [], config.default_agent || '');
        return sorted.length ? sorted[0] : null;
    }

    function sortModelNamesAssistantFirst(names, config) {
        var preferredAgent = preferredAssistantAgent(config || {});
        var preferredModel = String(config?.default_model || (preferredAgent && preferredAgent.model) || '').trim().toLowerCase();
        return (names || []).slice().sort(function(a, b) {
            var normalizedA = String(a || '').trim().toLowerCase();
            var normalizedB = String(b || '').trim().toLowerCase();
            if (preferredModel) {
                if (normalizedA === preferredModel && normalizedB !== preferredModel) return -1;
                if (normalizedB === preferredModel && normalizedA !== preferredModel) return 1;
            }
            return normalizedA.localeCompare(normalizedB);
        });
    }

    window.sortAgentsAssistantFirst = sortAgentsAssistantFirst;
    window.preferredAssistantAgent = preferredAssistantAgent;
    window.sortModelNamesAssistantFirst = sortModelNamesAssistantFirst;

    let currentMode = 'single';

    let currentCollabTaskId = null;

    let logs = [];



	let currentFilePath = '/';

	let currentFileList = [];

    let filteredFileList = [];

    let currentSessionSource = 'web';

    window.pendingTasks = window.pendingTasks || {};
    window.taskSessions = window.taskSessions || {};
    window.activeTaskBySession = window.activeTaskBySession || {};

    function updateTaskLoadingState() {
        var pending = window.pendingTasks || {};
        isLoading = Object.keys(pending).length > 0;
        var sendBtn = document.getElementById('sendBtn');
        if (sendBtn) {
            sendBtn.disabled = !isSessionEditable();
        }
        return isLoading;
    }
    window.updateTaskLoadingState = updateTaskLoadingState;

    function getTaskSession(taskId) {
        if (!taskId) return '';
        return (window.taskSessions && window.taskSessions[taskId]) || '';
    }
    window.getTaskSession = getTaskSession;

    function resolveProgressLogTaskId(taskId, sessionId) {
        taskId = (taskId || '').toString().trim();
        sessionId = (sessionId || '').toString().trim();
        if (!taskId) return '';
        if (taskId.indexOf('-t') !== -1) return taskId;
        if (!sessionId) sessionId = getTaskSession(taskId);
        if (!sessionId && typeof currentSessionId !== 'undefined' && /^t\d+$/.test(taskId)) {
            sessionId = currentSessionId || '';
        }
        if (sessionId && /^t\d+$/.test(taskId)) {
            return sessionId + '-' + taskId;
        }
        return taskId;
    }
    window.resolveProgressLogTaskId = resolveProgressLogTaskId;

    function progressLogDownloadUrl(taskId, sessionId) {
        var resolvedTaskId = resolveProgressLogTaskId(taskId, sessionId);
        if (!resolvedTaskId) return '';
        return '/api/task/progress-log/' + encodeURIComponent(resolvedTaskId);
    }
    window.progressLogDownloadUrl = progressLogDownloadUrl;

    function markTaskIdOnLoadingElement(loadingId, taskId, sessionId) {
        if (!loadingId || !taskId || typeof document === 'undefined') return;
        var loadingEl = document.getElementById(loadingId);
        if (!loadingEl) return;
        loadingEl.setAttribute('data-task-id', taskId);
        if (sessionId) loadingEl.setAttribute('data-session-id', sessionId);
        var bubble = loadingEl.querySelector('.message-bubble');
        if (bubble) {
            bubble.setAttribute('data-task-id', taskId);
            if (sessionId) bubble.setAttribute('data-session-id', sessionId);
        }
        var buttons = loadingEl.querySelectorAll('.log-export-btn');
        for (var i = 0; i < buttons.length; i++) {
            buttons[i].setAttribute('data-task-id', taskId);
            if (sessionId) buttons[i].setAttribute('data-session-id', sessionId);
        }
    }
    window.markTaskIdOnLoadingElement = markTaskIdOnLoadingElement;

    function getActiveTaskForSession(sessionId) {
        if (!sessionId) return '';
        var taskId = window.activeTaskBySession ? window.activeTaskBySession[sessionId] : '';
        if (!taskId) return '';
        if (window.pendingTasks && window.pendingTasks[taskId]) return taskId;
        delete window.activeTaskBySession[sessionId];
        return '';
    }
    window.getActiveTaskForSession = getActiveTaskForSession;

    function registerPendingTask(taskId, loadingId, sessionId) {
        if (!taskId) return;
        window.pendingTasks = window.pendingTasks || {};
        window.taskSessions = window.taskSessions || {};
        window.activeTaskBySession = window.activeTaskBySession || {};
        window.pendingTasks[taskId] = loadingId || '';
        if (sessionId) {
            window.taskSessions[taskId] = sessionId;
            window.activeTaskBySession[sessionId] = taskId;
        }
        markTaskIdOnLoadingElement(loadingId, taskId, sessionId);
        window._currentTaskId = taskId;
        updateTaskLoadingState();
    }
    window.registerPendingTask = registerPendingTask;

    function clearPendingTask(taskId) {
        if (!taskId) {
            updateTaskLoadingState();
            return '';
        }
        var sessionId = getTaskSession(taskId);
        if (window.pendingTasks) delete window.pendingTasks[taskId];
        if (window.taskSessions) delete window.taskSessions[taskId];
        if (sessionId && window.activeTaskBySession && window.activeTaskBySession[sessionId] === taskId) {
            delete window.activeTaskBySession[sessionId];
        }
        if (window._currentTaskId === taskId) {
            var remaining = Object.keys(window.pendingTasks || {});
            window._currentTaskId = remaining.length ? remaining[remaining.length - 1] : null;
        }
        updateTaskLoadingState();
        return sessionId;
    }
    window.clearPendingTask = clearPendingTask;

    function setDropdownMenuState(menu, open, preferredTrigger) {
        if (!menu) return;
        menu.classList.toggle('show', open);
        var trigger = preferredTrigger || document.querySelector('[aria-controls="' + menu.id + '"]');
        if (!trigger && menu.previousElementSibling && menu.previousElementSibling.tagName === 'BUTTON') {
            trigger = menu.previousElementSibling;
        }
        if (trigger) {
            trigger.setAttribute('aria-expanded', open ? 'true' : 'false');
            trigger.classList.toggle('is-open', open);
        }
    }

    function closeDropdownMenus() {
        document.querySelectorAll('.dropdown-menu').forEach(function(menu) {
            setDropdownMenuState(menu, false);
        });
    }
    window.closeDropdownMenus = closeDropdownMenus;

    function toggleDropdown(id, trigger, clickEvent) {
        var menu = document.getElementById(id);
        if (!menu) return;
        var wasOpen = menu.classList.contains('show');
        closeDropdownMenus();
        if (!wasOpen) setDropdownMenuState(menu, true, trigger);
        if (clickEvent) clickEvent.stopPropagation();
    }
    window.toggleDropdown = toggleDropdown;

    document.addEventListener('click', function(e) {
        var btn = e.target.closest && e.target.closest('[onclick]');
        var isToggle = btn && btn.getAttribute('onclick').indexOf('toggleDropdown') >= 0;
        if (!isToggle && !e.target.closest('.dropdown-menu.show')) {
            closeDropdownMenus();
        }
    });



	const style = document.createElement('style');

    document.head.appendChild(style);

    let currentTaskId = null;

    let currentLoadingId = null;

	let currentConfig = null;
	var settingsDirty = false;
	var settingsEditorDirty = false;
	var settingsSaving = false;

	function settingsUpdateSaveUI() {
		var state = document.getElementById('settingsSaveState');
		var button = document.getElementById('saveConfigButton');
		var hasPendingChanges = settingsDirty || settingsEditorDirty;
		if (state) {
			state.textContent = settingsSaving
				? settingsText('settingsSaving', '保存中…')
				: (hasPendingChanges ? settingsText('settingsUnsaved', '有未保存更改') : settingsText('settingsSaved', '已保存并生效'));
			state.style.color = hasPendingChanges ? '#d97706' : '#16a34a';
		}
		if (button) {
			button.disabled = settingsSaving;
			button.style.opacity = settingsSaving ? '0.65' : '1';
			button.style.cursor = settingsSaving ? 'wait' : 'pointer';
			button.textContent = settingsSaving
				? settingsText('settingsSaving', '保存中…')
				: settingsText('saveConfiguration', '保存并应用');
		}
	}

	function settingsSetDirty(dirty) {
		settingsDirty = !!dirty;
		settingsUpdateSaveUI();
	}

	function settingsSetEditorDirty(dirty) {
		settingsEditorDirty = !!dirty;
		settingsUpdateSaveUI();
	}

	function settingsSetSaving(saving) {
		settingsSaving = !!saving;
        ['configForm', 'settingsItemEditorBody'].forEach(function(id) { var el = document.getElementById(id); if (el) el.inert = settingsSaving; });
        var editorSave = document.getElementById('settingsItemEditorSave');
        if (editorSave) { editorSave.disabled = settingsSaving; editorSave.textContent = settingsText(settingsSaving ? 'settingsSaving' : 'settingsApplyEdit', settingsSaving ? '保存中…' : '保存并应用'); }
		settingsUpdateSaveUI();
	}

	function settingsConfirmDiscard(messageKey, fallback) {
		if (!settingsDirty && !settingsEditorDirty) return true;
		return confirm(settingsText(messageKey, fallback));
	}

	var backendStoppedToastAt = 0;

	function isBackendUnavailableError(err) {
		if (!err) return false;
		var msg = String(err.message || err || '').toLowerCase();
		return msg.indexOf('failed to fetch') >= 0 ||
			msg.indexOf('networkerror') >= 0 ||
			msg.indexOf('network request failed') >= 0 ||
			msg.indexOf('load failed') >= 0 ||
			msg.indexOf('err_connection_refused') >= 0 ||
			(err.name === 'TypeError' && (msg.indexOf('fetch') >= 0 || msg.indexOf('network') >= 0));
	}

	function showBackendStoppedNotice() {
		var el = document.getElementById('backendStoppedNotice');
		if (!el) {
			el = document.createElement('div');
			el.id = 'backendStoppedNotice';
			el.setAttribute('role', 'alert');
			el.style.cssText = [
				'position:fixed',
				'top:14px',
				'left:50%',
				'transform:translateX(-50%)',
				'z-index:2147483000',
				'display:flex',
				'align-items:center',
				'gap:10px',
				'max-width:calc(100vw - 32px)',
				'padding:10px 14px',
				'border-radius:8px',
				'background:#991b1b',
				'color:#fff',
				'box-shadow:0 12px 32px rgba(15,23,42,.28)',
				'font-size:calc(15.4px * var(--macs-font-scale, 1))',
				'line-height:1.4'
			].join(';');
			el.innerHTML = '<strong style="white-space:nowrap">后台服务已停止</strong><span>请重新启动 MACS 后刷新页面。</span>';
			document.body.appendChild(el);
		}
		el.style.display = 'flex';

		var now = Date.now();
		if (typeof showToast === 'function' && now - backendStoppedToastAt > 5000) {
			backendStoppedToastAt = now;
			showToast('后台服务已停止，请重新启动 MACS 后刷新页面。', 'error');
		}
	}

	function hideBackendStoppedNotice() {
		var el = document.getElementById('backendStoppedNotice');
		if (el) el.style.display = 'none';
	}

	window.showBackendStoppedNotice = showBackendStoppedNotice;
	window.reportBackendConnectionError = function(source) {
		showBackendStoppedNotice();
	};
	window.handleBackendFetchError = function(err) {
		if (!isBackendUnavailableError(err)) return false;
		showBackendStoppedNotice();
		return true;
	};

	(function patchBackendFetchMonitor() {
		if (!window.fetch || window.__openkittyFetchPatched) return;
		var nativeFetch = window.fetch.bind(window);
		window.fetch = function() {
			var requestInput = arguments[0];
			var monitorBackend = true;
			try {
				var requestURL = new URL(typeof requestInput === 'string' ? requestInput : requestInput.url, window.location.href);
				monitorBackend = requestURL.origin === window.location.origin;
			} catch (error) {}
			return nativeFetch.apply(window, arguments).then(function(resp) {
				if (monitorBackend) hideBackendStoppedNotice();
				return resp;
			}).catch(function(err) {
				if (monitorBackend && window.handleBackendFetchError) window.handleBackendFetchError(err);
				throw err;
			});
		};
		window.__openkittyFetchPatched = true;
	})();

	function accountText(key, fallback) {
		return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
	}

	var accountEndpoints = {
		checkLogin: '/api/account/me',
		logout: '/api/account/logout',
		summary: '/api/account/summary',
		center: 'https://www.sicurrent.com/login/'
	};
	var accountConfigPromise;
	var accountDisplayHintKey = 'openkitty_account_display_username';
	var accountSummaryController;
	var accountSummaryGeneration = 0;
	var accountSummary = {balance: null, keys: null, loading: false, verified: false};
	var communityFeedPromise;
	var communityFeedLoading = false;
	var communityFeedGeneration = 0;
	var communityWorkspaceDismissed = false;
	var communityWorkspaceRequested = false;
	var communityConversationActive = false;

	function safeAccountEndpoint(value) {
		try {
			var target = new URL(value, window.location.origin);
			return target.protocol === 'http:' || target.protocol === 'https:' ? target.href : '';
		} catch (error) {
			return '';
		}
	}

	function rememberAccountDisplayName(username) {
		username = String(username || '').trim();
		if (!username || username.length > 128) return;
		try { sessionStorage.setItem(accountDisplayHintKey, username); } catch (error) {}
	}

	function readAccountDisplayName() {
		try {
			var username = String(sessionStorage.getItem(accountDisplayHintKey) || '').trim();
			return username && username.length <= 128 ? username : '';
		} catch (error) {
			return '';
		}
	}

	function clearAccountDisplayName() {
		try { sessionStorage.removeItem(accountDisplayHintKey); } catch (error) {}
	}

	function loadAccountEndpoints() {
		if (accountConfigPromise) return accountConfigPromise;
		accountConfigPromise = fetch('/api/account/config', {
			method: 'GET',
			headers: { 'Accept': 'application/json' },
			cache: 'no-store'
		}).then(function(response) {
			if (!response.ok) throw new Error('account config failed: ' + response.status);
			return response.json();
		}).then(function(data) {
			accountEndpoints.center = safeAccountEndpoint(data.account_center_url) || accountEndpoints.center;
			return accountEndpoints;
		}).catch(function(error) {
			console.warn('[account] Could not load account config:', error);
			return accountEndpoints;
		});
		return accountConfigPromise;
	}

	function setCommunityWorkspaceVisible(visible) {
		var workspace = document.getElementById('communityWorkspace');
		if (!workspace) return;
		var hasMessages = !!document.getElementById('messagesArea')?.querySelector('.message');
		workspace.hidden = !visible || communityConversationActive || hasMessages;
	}

	function setCommunityConversationState(active) {
		communityConversationActive = !!active;
		setCommunityWorkspaceVisible(communityWorkspaceRequested && !communityWorkspaceDismissed);
	}
	window.setCommunityConversationState = setCommunityConversationState;

	function hideCommunityWorkspace() {
		communityWorkspaceRequested = false;
		communityFeedGeneration++;
		communityFeedPromise = null;
		communityFeedLoading = false;
		setCommunityWorkspaceVisible(false);
		var feed = document.getElementById('communityFeed');
		if (feed) feed.replaceChildren();
		var refreshButton = document.getElementById('communityWorkspaceRefreshButton');
		if (refreshButton) refreshButton.disabled = false;
	}

	function communityPostDate(value) {
		if (!value) return '';
		var date = new Date(value);
		if (Number.isNaN(date.getTime())) return value;
		try {
			return new Intl.DateTimeFormat(document.documentElement.lang === 'en' ? 'en' : 'zh-CN', {
				month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit'
			}).format(date);
		} catch (error) {
			return date.toLocaleString();
		}
	}

	function renderCommunityFeed(data) {
		var feed = document.getElementById('communityFeed');
		var updated = document.getElementById('communityWorkspaceUpdated');
		if (!feed) return;
		feed.replaceChildren();
		var posts = data && Array.isArray(data.posts) ? data.posts : [];
		if (!posts.length) {
			var empty = document.createElement('div');
			empty.className = 'community-feed-empty';
			empty.textContent = '暂时没有可显示的科技帖子，请稍后刷新。';
			feed.appendChild(empty);
			return;
		}
		posts.forEach(function(post) {
			var card = document.createElement('a');
			card.className = 'community-post-card';
			var sourceURL = Number.isSafeInteger(Number(post.id)) && Number(post.id) > 0
				? '/web/community.html?id=' + encodeURIComponent(post.id) : '';
			card.href = sourceURL || '#';
			if (sourceURL) {
				card.setAttribute('aria-haspopup', 'dialog');
				card.addEventListener('click', function(event) {
					if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
					event.preventDefault();
					if (typeof window.openCommunityReader === 'function') window.openCommunityReader(post.id, card);
				});
			} else {
				card.addEventListener('click', function(event) { event.preventDefault(); });
			}

			var badge = document.createElement('span');
			badge.className = 'community-post-badge';
			badge.textContent = '科技动态';
			var title = document.createElement('strong');
			title.textContent = post.title || '科技社区动态';
			var summary = document.createElement('span');
			summary.className = 'community-post-summary';
			summary.textContent = post.summary || '';
			var meta = document.createElement('span');
			meta.className = 'community-post-meta';
			var author = String(post.author || 'Nova 社区').trim();
			var stamp = communityPostDate(post.created_at);
			meta.textContent = stamp ? author + ' · ' + stamp : author;
			var open = document.createElement('span');
			open.className = 'community-post-open';
			open.textContent = '阅读全文 →';

			card.appendChild(badge);
			card.appendChild(title);
			card.appendChild(summary);
			card.appendChild(meta);
			card.appendChild(open);
			feed.appendChild(card);
		});
		if (updated) {
			var fetchedAt = communityPostDate(data.fetched_at);
			updated.textContent = (data.stale ? '离线缓存' : '已同步') + (fetchedAt ? ' · ' + fetchedAt : '');
			updated.classList.toggle('is-stale', !!data.stale);
		}
	}

	function renderCommunityFeedError(error) {
		var feed = document.getElementById('communityFeed');
		var updated = document.getElementById('communityWorkspaceUpdated');
		if (!feed) return;
		feed.replaceChildren();
		var state = document.createElement('div');
		state.className = 'community-feed-empty community-feed-error';
		state.textContent = error && error.message ? error.message : '科技社区暂时无法连接。';
		var retry = document.createElement('button');
		retry.type = 'button';
		retry.textContent = '重新获取';
		retry.addEventListener('click', refreshCommunityWorkspace);
		state.appendChild(retry);
		feed.appendChild(state);
		if (updated) updated.textContent = '同步失败';
	}

	function loadCommunityFeed(force) {
		if (communityFeedPromise && (!force || communityFeedLoading)) return communityFeedPromise;
		var requestGeneration = ++communityFeedGeneration;
		var feed = document.getElementById('communityFeed');
		var updated = document.getElementById('communityWorkspaceUpdated');
		var refreshButton = document.getElementById('communityWorkspaceRefreshButton');
		communityFeedLoading = true;
		if (refreshButton) refreshButton.disabled = true;
		if (updated) {
			updated.textContent = '正在同步…';
			updated.classList.remove('is-stale');
		}
		if (feed) {
			feed.replaceChildren();
			var loading = document.createElement('div');
			loading.className = 'community-feed-loading';
			loading.textContent = '正在获取科技社区动态…';
			feed.appendChild(loading);
		}
		communityFeedPromise = fetch('/api/community/feed?limit=8', {
			method: 'GET',
			headers: { 'Accept': 'application/json' },
			cache: 'no-store'
		}).then(function(response) {
			if (!response.ok) throw new Error('科技社区暂时不可用（' + response.status + '）');
			return response.json();
		}).then(function(data) {
			if (!data || !data.ok) throw new Error((data && data.error) || '科技社区暂时不可用');
			if (requestGeneration !== communityFeedGeneration) return data;
			renderCommunityFeed(data);
			return data;
		}).catch(function(error) {
			if (requestGeneration === communityFeedGeneration) {
				communityFeedPromise = null;
				renderCommunityFeedError(error);
			}
			throw error;
		}).finally(function() {
			if (requestGeneration !== communityFeedGeneration) return;
			communityFeedLoading = false;
			if (refreshButton) refreshButton.disabled = false;
		});
		return communityFeedPromise;
	}

	function openCommunityWorkspace() {
		communityWorkspaceDismissed = false;
		communityWorkspaceRequested = true;
		setCommunityWorkspaceVisible(true);
		loadCommunityFeed(false).catch(function(error) {
			console.warn('[community] Could not load feed:', error);
		});
	}

	function closeCommunityWorkspace() {
		communityWorkspaceDismissed = true;
		setCommunityWorkspaceVisible(false);
	}

	function refreshCommunityWorkspace() {
		loadCommunityFeed(true).catch(function(error) {
			console.warn('[community] Could not refresh feed:', error);
		});
	}

	function setAccountMenuOpen(open, focusMenu) {
		var menu = document.getElementById('topbarAccountMenu');
		var button = document.getElementById('topbarUserCenterButton');
		if (!menu || !button) return;
		menu.hidden = !open;
		button.setAttribute('aria-expanded', open ? 'true' : 'false');
		button.classList.toggle('is-open', open);
		if (open && focusMenu) {
			var firstItem = menu.querySelector('[role="menuitem"]');
			if (firstItem) firstItem.focus();
		}
		if (open && accountSummary.verified) loadAccountSummary();
	}

	function toggleAccountMenu(event) {
		if (event) {
			event.preventDefault();
			event.stopPropagation();
		}
		var menu = document.getElementById('topbarAccountMenu');
		setAccountMenuOpen(!!(menu && menu.hidden), !!(event && event.detail === 0));
	}

	function closeAccountMenu(focusButton) {
		setAccountMenuOpen(false, false);
		if (focusButton) {
			var button = document.getElementById('topbarUserCenterButton');
			if (button) button.focus();
		}
	}

	function renderAccountSummary() {
		var balance = document.getElementById('sidebarAccountBalance');
		var keys = document.getElementById('sidebarAccountApiKeys');
		var caption = document.getElementById('sidebarAccountCaption');
		var loading = accountText('accountReading', '读取中');
		if (balance) {
			balance.textContent = accountSummary.loading ? loading : (accountSummary.balance === null ? '—' : accountSummary.balance.toFixed(2)+' '+accountText('accountPoints', '积分'));
			balance.title = balance.textContent;
		}
		if (keys) {
			keys.textContent = accountSummary.loading ? loading : (accountSummary.keys === null ? '—' : accountSummary.keys.active+'/'+accountSummary.keys.total);
			keys.title = accountSummary.loading || accountSummary.keys === null ? keys.textContent : accountText('accountKeysCountTitle', '已启用 / 总数：{active} / {total}').replace('{active}', String(accountSummary.keys.active)).replace('{total}', String(accountSummary.keys.total));
		}
		if (caption) {
			caption.hidden = accountSummary.verified && (accountSummary.loading || accountSummary.balance !== null);
			caption.textContent = caption.hidden ? '' : (accountSummary.verified ? accountText('accountSummaryUnavailable', '积分暂未读到，点击重试') : accountText('accountPending', '请重新登录确认账号'));
		}
	}

	async function loadAccountSummary() {
		if (!accountSummary.verified || accountSummary.loading) return;
		var generation = accountSummaryGeneration;
		var controller = new AbortController();
		accountSummaryController = controller;
		accountSummary.loading = true;
		renderAccountSummary();
		var timeout = setTimeout(function() { controller.abort(); }, 10000);
		try {
			var response = await fetch(accountEndpoints.summary, {method: 'GET', credentials: 'same-origin', cache: 'no-store', signal: controller.signal, headers: {'Accept': 'application/json'}});
			if (generation !== accountSummaryGeneration) return;
			if (response.status === 401) { clearAccountDisplayName(); renderTopbarAccount(null); return; }
			if (!response.ok) throw new Error('Account summary unavailable');
			var data = await response.json();
			if (generation !== accountSummaryGeneration) return;
			if (!data.ok || !data.authenticated) throw new Error('Account summary unavailable');
			accountSummary.balance = typeof data.balance === 'number' && Number.isFinite(data.balance) ? data.balance : null;
			accountSummary.keys = data.api_keys && Number.isInteger(data.api_keys.active) && Number.isInteger(data.api_keys.total) ? data.api_keys : null;
		} catch (error) {
			// Missing summary data leaves a placeholder; account access remains available.
			if (generation === accountSummaryGeneration) { accountSummary.balance = null; accountSummary.keys = null; }
		} finally {
			clearTimeout(timeout);
			if (generation === accountSummaryGeneration) {
				accountSummary.loading = false;
				accountSummaryController = null;
				renderAccountSummary();
			}
		}
	}

	function renderTopbarAccount(user) {
		var checking = document.getElementById('topbarAuthChecking');
		var loginButton = document.getElementById('topbarLoginButton');
		var userState = document.getElementById('topbarUserState');
		var userName = document.getElementById('topbarUserName');
		var avatar = document.getElementById('topbarUserAvatar');
		var userCenterButton = document.getElementById('topbarUserCenterButton');
		var accountCenterLink = document.getElementById('topbarAccountCenterLink');
		var accountUsername = document.getElementById('topbarAccountUsername');
		if (!loginButton || !userState) return;
		accountSummaryGeneration++;
		if (accountSummaryController) accountSummaryController.abort();
		accountSummary = {balance: null, keys: null, loading: false, verified: !!(user && user.verified)};
		renderAccountSummary();

		if (checking) checking.hidden = true;
		var username = user && String(user.username || user.display_name || '').trim();
		var identity = user && user.verified ? username : '';
		if (window.composerAccountIdentity !== identity) {
			window.composerAccountIdentity = identity;
			document.dispatchEvent(new CustomEvent('openkitty:accountchange'));
		}
		if (!username) {
			closeAccountMenu(false);
			loginButton.hidden = false;
			userState.hidden = true;
			userState.removeAttribute('data-username');
			hideCommunityWorkspace();
			return;
		}

		loginButton.hidden = true;
		userState.hidden = false;
		userState.setAttribute('data-username', username);
		rememberAccountDisplayName(username);
		var centerTitle = accountText('accountCenterTitle', '打开用户中心');
		if (userName) userName.textContent = username;
		if (avatar) avatar.textContent = username.charAt(0).toUpperCase() || 'U';
		if (accountUsername) accountUsername.textContent = username;
		if (userCenterButton) {
			userCenterButton.title = centerTitle + ' · ' + username;
			userCenterButton.setAttribute('aria-label', centerTitle + ' · ' + username);
		}
		if (accountCenterLink) {
			accountCenterLink.href = safeAccountEndpoint(accountEndpoints.center) || 'https://www.sicurrent.com/login/';
			accountCenterLink.title = centerTitle;
		}
		var walletLink = document.getElementById('topbarAccountWalletLink');
		var centerURL = safeAccountEndpoint(accountEndpoints.center);
		if (walletLink) {
			walletLink.hidden = !centerURL;
			if (centerURL) walletLink.href = new URL('/wallet/', centerURL).href;
		}
		if (accountSummary.verified) loadAccountSummary();
		var greeting = document.getElementById('communityWorkspaceGreeting');
		if (greeting) greeting.textContent = '欢迎回来，' + username;
		if (!communityWorkspaceDismissed) openCommunityWorkspace();
	}

	function openAccountLogin() {
		window.openAccountLoginModal();
	}

	async function refreshTopbarAccount() {
		var checking = document.getElementById('topbarAuthChecking');
		var loginButton = document.getElementById('topbarLoginButton');
		var userState = document.getElementById('topbarUserState');
		if (checking) checking.hidden = false;
		closeAccountMenu(false);
		if (loginButton) loginButton.hidden = true;
		if (userState) userState.hidden = true;

		try {
			var endpoints = await loadAccountEndpoints();
			var response = await fetch(endpoints.checkLogin, {
				method: 'GET',
				headers: { 'Accept': 'application/json' },
				credentials: 'include',
				cache: 'no-store'
			});
			if (!response.ok) {
				if (response.status === 401) { clearAccountDisplayName(); renderTopbarAccount(null); return; }
				var displayName = readAccountDisplayName();
				renderTopbarAccount(displayName ? { username: displayName } : null);
				return;
			}
			var data = await response.json();
			if (data && data.authenticated && data.user && data.user.username) {
				renderTopbarAccount({ username: data.user.username, verified: true });
				return;
			}
			if (data && data.authenticated === false) {
				clearAccountDisplayName();
				renderTopbarAccount(null);
				return;
			}
			var displayName = readAccountDisplayName();
			renderTopbarAccount(displayName ? { username: displayName } : null);
		} catch (error) {
			console.warn('[account] Could not check login state:', error);
			var displayName = readAccountDisplayName();
			renderTopbarAccount(displayName ? { username: displayName } : null);
		}
	}

	async function logoutAccount() {
		var logoutButton = document.getElementById('topbarLogoutButton');
		closeAccountMenu(false);
		if (logoutButton) logoutButton.disabled = true;
		try {
			var endpoints = await loadAccountEndpoints();
			var response = await fetch(endpoints.logout, {
				method: 'POST',
				headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
				credentials: 'include',
				body: '{}'
			});
			if (!response.ok && response.status !== 401) throw new Error('logout failed: ' + response.status);
			clearAccountDisplayName();
			renderTopbarAccount(null);
			if (typeof showToast === 'function') showToast(accountText('accountLoggedOut', '已退出登录'), 'success');
		} catch (error) {
			console.warn('[account] Logout failed:', error);
			if (typeof showToast === 'function') showToast(accountText('accountLogoutFailed', '退出失败，请稍后重试'), 'error');
		} finally {
			if (logoutButton) logoutButton.disabled = false;
		}
	}

	window.openAccountLogin = openAccountLogin;
	window.toggleAccountMenu = toggleAccountMenu;
	window.closeAccountMenu = closeAccountMenu;
	window.refreshTopbarAccount = refreshTopbarAccount;
	window.addEventListener('focus', function() { if (accountSummary.verified) loadAccountSummary(); });
	window.logoutAccount = logoutAccount;
	window.openCommunityWorkspace = openCommunityWorkspace;
	window.closeCommunityWorkspace = closeCommunityWorkspace;
	window.refreshCommunityWorkspace = refreshCommunityWorkspace;
	var conversationArea = document.getElementById('messagesArea');
	if (conversationArea) new MutationObserver(function() {
		setCommunityWorkspaceVisible(communityWorkspaceRequested && !communityWorkspaceDismissed);
	}).observe(conversationArea, {childList: true, subtree: true});
	if (document.readyState === 'loading') {
		document.addEventListener('DOMContentLoaded', refreshTopbarAccount, { once: true });
	} else {
		refreshTopbarAccount();
	}
	document.addEventListener('openkitty:languagechange', function() {
		var userState = document.getElementById('topbarUserState');
		if (!userState || userState.hidden) return;
		var username = userState.getAttribute('data-username') || '';
		var userName = document.getElementById('topbarUserName');
		var userCenterButton = document.getElementById('topbarUserCenterButton');
		var accountCenterLink = document.getElementById('topbarAccountCenterLink');
		var centerTitle = accountText('accountCenterTitle', '打开用户中心');
		if (userName) userName.textContent = username;
		renderAccountSummary();
		if (userCenterButton) {
			userCenterButton.title = centerTitle + ' · ' + username;
			userCenterButton.setAttribute('aria-label', centerTitle + ' · ' + username);
		}
		if (accountCenterLink) accountCenterLink.title = centerTitle;
	});
	document.addEventListener('click', function(event) {
		var userState = document.getElementById('topbarUserState');
		if (userState && !userState.hidden && !userState.contains(event.target)) closeAccountMenu(false);
	});
	document.addEventListener('keydown', function(event) {
		if (event.key !== 'Escape') return;
		var menu = document.getElementById('topbarAccountMenu');
		if (menu && !menu.hidden) {
			event.preventDefault();
			closeAccountMenu(true);
		}
	});



	let currentTaskTab = 'all';
	var workCenterItems = [];
	var workCenterSelectedKey = '';
	var workCenterPollTimer = null;
	var workCenterLoading = false;
	var workCenterLastRefresh = null;

	function workCenterText(key, fallback) {
		return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
	}

	function openTaskPanel() {
		var modal = document.getElementById('taskModal');
		if (!modal) return;
		if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(modal);
		modal.classList.add('open');
		switchTaskTab(currentTaskTab || 'all');
		loadTasks();
		if (!workCenterPollTimer) {
			workCenterPollTimer = setInterval(function() {
				if (modal.classList.contains('open')) loadTasks(false, true);
			}, 15000);
		}
		setTimeout(function() {
			var search = document.getElementById('workCenterSearch');
			if (search) search.focus();
		}, 80);
	}

	function closeTaskPanel() {
		var modal = document.getElementById('taskModal');
		if (modal) modal.classList.remove('open');
		if (workCenterPollTimer) {
			clearInterval(workCenterPollTimer);
			workCenterPollTimer = null;
		}
	}

	function switchTaskTab(tab) {
		currentTaskTab = tab || 'all';
		var tabs = document.querySelectorAll('#taskModal .task-tab[data-work-filter]');
		for (var i = 0; i < tabs.length; i++) {
			var active = tabs[i].getAttribute('data-work-filter') === currentTaskTab;
			tabs[i].classList.toggle('active', active);
			tabs[i].setAttribute('aria-selected', active ? 'true' : 'false');
		}
		renderWorkCenter();
	}

	function filterWorkCenter() {
		renderWorkCenter();
	}



	function openHelpModal() {

		document.getElementById('helpModal').classList.add('active');

	}

	function scrollHelpContactSection() {
		var target = document.getElementById('help-contact');
		if (!target) return;
		target.scrollIntoView({ behavior: 'smooth', block: 'start' });
		target.style.boxShadow = '0 0 0 3px rgba(220,38,38,0.16)';
		setTimeout(function() { target.style.boxShadow = ''; }, 1600);
	}

	function openHelpContactSection() {
		openHelpModal();
		setTimeout(scrollHelpContactSection, 80);
	}

	window.openHelpContactSection = openHelpContactSection;
	window.openAEContactHelp = openHelpContactSection;

	if (window.location.hash === '#help-contact' || new URLSearchParams(window.location.search).get('help') === 'contact') {
		setTimeout(openHelpContactSection, 160);
	}



function closeHelpModal() {

		document.getElementById('helpModal').classList.remove('active');

	}



	async function fetchWorkCenterJSON(url) {
		try {
			var response = await fetch(url, { headers: { Accept: 'application/json' } });
			if (!response.ok) throw new Error('HTTP ' + response.status);
			return { ok: true, data: await response.json() };
		} catch (error) {
			console.warn('[work-center] request failed:', url, error);
			return { ok: false, error: error };
		}
	}

	function workCenterPlainText(value, limit) {
		var text = value == null ? '' : String(value);
		text = text.replace(/```[\s\S]*?```/g, ' ').replace(/[#*_`>|\[\]()]/g, ' ').replace(/\s+/g, ' ').trim();
		if (!limit || text.length <= limit) return text;
		return text.substring(0, Math.max(1, limit - 1)).trim() + '…';
	}

	function workCenterRealID(value) {
		if (typeof value === 'string') return value.trim() ? value : '';
		return typeof value === 'number' && Number.isFinite(value) ? String(value) : '';
	}

	function workCenterTimeValue(value) {
		var time = value ? new Date(value).getTime() : 0;
		return isNaN(time) ? 0 : time;
	}

	function workCenterFormatTime(value) {
		var timestamp = workCenterTimeValue(value);
		if (!timestamp) return workCenterText('workCenterUnknownTime', '未知');
		var date = new Date(timestamp);
		var now = new Date();
		var sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
		var locale = window.openKittyLanguage === 'en' ? 'en-US' : 'zh-CN';
		if (sameDay) return (window.openKittyLanguage === 'en' ? 'Today ' : '今天 ') + date.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', hour12: false });
		return date.toLocaleDateString(locale, { month: '2-digit', day: '2-digit' }) + ' ' + date.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit', hour12: false });
	}

	function workCenterStatus(status) {
		status = String(status || 'pending').toLowerCase();
		var map = {
			running: ['running', 'workCenterStatusRunning', '执行中'],
			pending: ['pending', 'workCenterStatusPending', '等待执行'],
			pausing: ['pending', 'workCenterStatusPausing', '正在暂停'],
			paused: ['attention', 'workCenterStatusPaused', '已暂停'],
			waiting_approval: ['attention', 'workCenterStatusApproval', '等待审批'],
			degraded: ['attention', 'workCenterStatusReview', '需要复核'],
			unavailable: ['attention', 'workCenterStatusUnavailable', '暂不可用'],
			failed: ['failed', 'workCenterStatusFailed', '失败'],
			completed: ['completed', 'workCenterStatusCompleted', '已完成'],
			stopped: ['stopped', 'workCenterStatusStopped', '已停止'],
			cancelled: ['stopped', 'workCenterStatusStopped', '已停止'],
			canceled: ['stopped', 'workCenterStatusStopped', '已停止']
		};
		var item = map[status] || ['pending', 'workCenterStatusPending', status || '等待执行'];
		return { raw: status, tone: item[0], label: workCenterText(item[1], item[2]) };
	}

	function workCenterFilterGroup(status) {
		status = String(status || '').toLowerCase();
		if (status === 'running' || status === 'pending' || status === 'pausing') return 'running';
		if (status === 'paused' || status === 'waiting_approval' || status === 'degraded' || status === 'failed' || status === 'unavailable') return 'attention';
		return 'finished';
	}

	function workCenterSourceMeta(source) {
		var sources = {
			pipeline: ['workCenterPipeline', '工作流'],
			employee: ['workCenterEmployee', 'AI 员工'],
			task: ['workCenterConversation', '对话任务'],
			scheduled: ['workCenterScheduled', '自动任务']
		};
		var value = sources[source] || sources.task;
		return { label: workCenterText(value[0], value[1]) };
	}

	function workCenterSourceIcon(source) {
		var paths = {
			pipeline: '<circle cx="6" cy="5" r="2"/><circle cx="18" cy="12" r="2"/><circle cx="6" cy="19" r="2"/><path d="M8 5h2a4 4 0 0 1 4 4v0a3 3 0 0 0 3 3M8 19h2a4 4 0 0 0 4-4v0a3 3 0 0 1 3-3"/>',
			employee: '<circle cx="12" cy="7" r="3"/><path d="M5 20v-2a6 6 0 0 1 6-6h2a6 6 0 0 1 6 6v2"/>',
			task: '<path d="M21 12a8 8 0 0 1-8 8 9 9 0 0 1-4-.9L3 21l1.8-4.5A8 8 0 1 1 21 12Z"/>',
			scheduled: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>'
		};
		return '<svg viewBox="0 0 24 24" aria-hidden="true">' + (paths[source] || paths.task) + '</svg>';
	}

	function workCenterTaskItem(task) {
		var message = workCenterPlainText(task.message || '', 180);
		var progress = task.pipeline_progress;
		var progressText = task.error || '';
		if (!progressText && progress) {
			progressText = (progress.current_step_name || progress.pipeline_name || workCenterText('workCenterExecuting', '正在执行'));
			if (progress.total_steps) progressText += ' · ' + (progress.current_step || 0) + '/' + progress.total_steps;
		}
		if (!progressText) progressText = task.duration || workCenterText('workCenterQueued', '已进入任务队列');
		var status = String(task.status || 'pending').toLowerCase();
		return {
			key: 'task:' + task.id,
			id: workCenterRealID(task.id),
			source: 'task',
			title: message || workCenterText('workCenterUntitledTask', '未命名任务'),
			description: task.type || 'chat',
			owner: task.agent_name || workCenterText('workCenterDefaultAgent', '默认 Agent'),
			status: status,
			canResume: status === 'paused' && task.can_resume !== false && task.recovery_state !== 'resumed',
			resumedByTaskId: String(task.resumed_by_task_id || ''),
			progress: workCenterPlainText(progressText, 90),
			error: workCenterPlainText(task.error || '', 240),
			createdAt: task.created_at,
			updatedAt: task.completed_at || task.created_at,
			artifacts: [],
			action: 'task'
		};
	}

	function workCenterPipelineItem(exec) {
		var steps = Array.isArray(exec.steps) ? exec.steps : [];
		var completed = steps.filter(function(step) { return step && (step.status === 'completed' || step.status === 'degraded'); }).length;
		var active = steps.find(function(step) { return step && (step.status === 'running' || step.status === 'pausing' || step.status === 'paused' || step.status === 'waiting_approval' || step.status === 'failed'); });
		var progress = active && active.name ? active.name : '';
		if (!progress && steps.length) progress = completed + '/' + steps.length + ' ' + workCenterText('workCenterStepsCompleted', '步骤完成');
		if (!progress) progress = workCenterText('workCenterReady', '已创建执行记录');
		var artifacts = Array.isArray(exec.artifacts) ? exec.artifacts : [];
		if (!artifacts.length && exec.delivery && Array.isArray(exec.delivery.artifacts)) artifacts = exec.delivery.artifacts;
		return {
			key: 'pipeline:' + exec.id,
			id: workCenterRealID(exec.id),
			linkedTaskId: workCenterRealID(exec.task_id),
			source: 'pipeline',
			title: workCenterPlainText(exec.pipeline_name || exec.input || '', 100) || workCenterText('workCenterUntitledTask', '未命名任务'),
			description: workCenterPlainText(exec.input || '', 180),
			owner: exec.employee_name || exec.employee_id || workCenterText('workCenterPipelineOwner', '工作流'),
			status: String(exec.status || 'pending').toLowerCase(),
			progress: workCenterPlainText(progress, 90),
			error: workCenterPlainText((exec.completion_reason && exec.completion_reason.message) || '', 240),
			createdAt: exec.created_at,
			updatedAt: exec.completed_at || exec.updated_at || exec.created_at,
			artifacts: artifacts.map(function(artifact) {
				return { name: workCenterPlainText((artifact.path || '').split(/[\\/]/).pop() || workCenterText('workCenterArtifact', '产物'), 48), url: artifact.download_url || '' };
			}),
			action: 'pipeline'
		};
	}

	function workCenterEmployeeItem(employee, record) {
		return {
			key: 'employee:' + (record.id || record.exec_id || employee.employee_id) + ':' + (record.created_at || ''),
			id: workCenterRealID(record.exec_id) || workCenterRealID(record.id),
			source: 'employee',
			title: workCenterPlainText(record.task || employee.role || employee.name, 100),
			description: employee.role || employee.note || '',
			owner: employee.name || employee.at_name || employee.employee_id || workCenterText('workCenterEmployee', 'AI 员工'),
			status: String(record.status || 'pending'),
			progress: workCenterPlainText(record.result || record.task || workCenterText('workCenterQueued', '已进入任务队列'), 90),
			error: '',
			createdAt: record.created_at,
			updatedAt: record.updated_at || record.created_at,
			artifacts: [],
			hasResult: !!record.result,
			action: record.exec_id ? 'pipeline' : 'employee'
		};
	}

	function workCenterScheduledItem(todo) {
		var progress = todo.last_error || todo.last_result || '';
		if (!progress && todo.next_run_at) progress = workCenterText('workCenterNextRun', '下次运行') + ' ' + workCenterFormatTime(todo.next_run_at);
		if (!progress) progress = workCenterText('workCenterScheduledReady', '等待计划触发');
		return {
			key: 'scheduled:' + (todo.todo_id || todo.id),
			id: workCenterRealID(todo.todo_id) || workCenterRealID(todo.id),
			source: 'scheduled',
			title: workCenterPlainText(todo.title || todo.description, 100) || workCenterText('workCenterUntitledTask', '未命名任务'),
			description: workCenterPlainText(todo.description || todo.pipeline_name || '', 180),
			owner: todo.agent_name || todo.pipeline_name || todo.team_name || workCenterText('workCenterScheduler', '调度器'),
			status: String(todo.status || 'pending'),
			progress: workCenterPlainText(progress, 90),
			error: workCenterPlainText(todo.last_error || '', 240),
			createdAt: todo.created_at,
			updatedAt: todo.updated_at || todo.last_run_at || todo.created_at,
			artifacts: [],
			hasResult: !!todo.output_data,
			action: 'scheduled'
		};
	}

	function buildWorkCenterItems(taskData, historyData, pipelineData, employeeData, todoData) {
		var items = [];
		var pipelineIDs = Object.create(null);
		var linkedTaskIDs = Object.create(null);
		var executions = pipelineData && Array.isArray(pipelineData.executions) ? pipelineData.executions : [];
		executions.forEach(function(exec) {
			if (!exec) return;
			if (workCenterRealID(exec.id)) pipelineIDs[workCenterRealID(exec.id)] = true;
			if (workCenterRealID(exec.task_id)) linkedTaskIDs[workCenterRealID(exec.task_id)] = true;
			items.push(workCenterPipelineItem(exec));
		});

		var employees = employeeData && Array.isArray(employeeData.employees) ? employeeData.employees : [];
		employees.forEach(function(employee) {
			(Array.isArray(employee.recent_work) ? employee.recent_work : []).forEach(function(record) {
				if (!record) return;
				if (record.exec_id && pipelineIDs[String(record.exec_id)]) return;
				items.push(workCenterEmployeeItem(employee, record));
			});
		});

		var taskMap = Object.create(null);
		var tasksWithoutID = [];
		['running', 'pending', 'pausing', 'paused', 'completed', 'stopped', 'failed'].forEach(function(status) {
			var list = taskData && Array.isArray(taskData[status]) ? taskData[status] : [];
			list.forEach(function(task) {
				if (!task) return;
				var id = workCenterRealID(task.id);
				if (id) taskMap[id] = task;
				else tasksWithoutID.push(task);
			});
		});
		var history = historyData && Array.isArray(historyData.tasks) ? historyData.tasks : [];
		history.forEach(function(task) {
			if (!task) return;
			var id = workCenterRealID(task.id);
			if (id && !taskMap[id]) taskMap[id] = task;
			else if (!id) tasksWithoutID.push(task);
		});
		Object.keys(taskMap).forEach(function(id) {
			if (linkedTaskIDs[id]) return;
			items.push(workCenterTaskItem(taskMap[id]));
		});
		tasksWithoutID.forEach(function(task) { items.push(workCenterTaskItem(task)); });

		var todos = todoData && Array.isArray(todoData.todos) ? todoData.todos : [];
		todos.forEach(function(todo) { if (todo) items.push(workCenterScheduledItem(todo)); });

		// Keys identify rows only; a missing source ID remains empty in the UI and clipboard.
		var rowKeys = Object.create(null);
		items.forEach(function(item, index) {
			if (!item.id || rowKeys[item.key]) item.key = item.source + ':row:' + index;
			rowKeys[item.key] = true;
		});

		items.sort(function(a, b) { return workCenterTimeValue(b.updatedAt || b.createdAt) - workCenterTimeValue(a.updatedAt || a.createdAt); });
		return items;
	}

	function workCenterMatchesTab(item) {
		if (currentTaskTab === 'all') return true;
		return workCenterFilterGroup(item.status) === currentTaskTab;
	}

	function workCenterFilteredItems() {
		var search = document.getElementById('workCenterSearch');
		var source = document.getElementById('workCenterSource');
		var query = search ? String(search.value || '').trim().toLowerCase() : '';
		var sourceValue = source ? source.value : 'all';
		return workCenterItems.filter(function(item) {
			if (!workCenterMatchesTab(item)) return false;
			if (sourceValue !== 'all' && item.source !== sourceValue) return false;
			if (!query) return true;
			return [item.title, item.description, item.owner, item.progress, item.id, item.linkedTaskId].join(' ').toLowerCase().indexOf(query) !== -1;
		});
	}

	function workCenterArtifactHTML(item, expanded) {
		var artifacts = Array.isArray(item.artifacts) ? item.artifacts : [];
		if (artifacts.length) {
			var first = artifacts[0];
			var label = escapeHtml(first.name || workCenterText('workCenterArtifact', '产物'));
			if (first.url) return '<a class="work-center-artifact-link" href="' + escapeAttr(first.url) + '" onclick="event.stopPropagation()" target="_blank" rel="noopener">' + label + (expanded && artifacts.length > 1 ? ' +' + (artifacts.length - 1) : '') + '</a>';
			return '<span class="work-center-artifact-text">' + label + '</span>';
		}
		if (item.hasResult) return '<span class="work-center-artifact-text">' + escapeHtml(workCenterText('workCenterHasResult', '已有结果')) + '</span>';
		return '<span class="work-center-empty-value">—</span>';
	}

	function workCenterIDCellHTML(item) {
		if (!item.id) return '<span class="work-center-id-cell"><span class="work-center-empty-value">—</span></span>';
		var copyLabel = workCenterText('workCenterCopyID', '复制任务 ID');
		return '<span class="work-center-id-cell" onclick="event.stopPropagation()" onkeydown="event.stopPropagation()">' +
			'<code class="work-center-id-value" tabindex="-1" title="' + escapeAttr(item.id) + '">' + escapeHtml(item.id) + '</code>' +
			'<button class="work-center-id-copy" type="button" aria-label="' + escapeAttr(copyLabel) + '" title="' + escapeAttr(copyLabel) + '" onclick="copyWorkCenterID(event,' + jsCallArg(item.key) + ')">' +
			'<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3"/></svg></button>' +
			'<span class="work-center-id-feedback" role="status" hidden></span></span>';
	}

	function selectWorkCenterID(cell) {
		var value = cell && cell.querySelector('.work-center-id-value');
		if (!value) return;
		value.focus({ preventScroll: true });
		var selection = window.getSelection();
		if (!selection) return;
		var range = document.createRange();
		range.selectNodeContents(value);
		selection.removeAllRanges();
		selection.addRange(range);
	}

	async function copyWorkCenterID(event, key) {
		if (event) { event.preventDefault(); event.stopPropagation(); }
		var item = workCenterItems.find(function(candidate) { return candidate.key === key; });
		var cell = event && event.currentTarget ? event.currentTarget.closest('.work-center-id-cell') : null;
		if (!item || !item.id || !cell) return;
		var feedback = cell.querySelector('.work-center-id-feedback');
		var copied = false;
		try {
			if (!navigator.clipboard || typeof navigator.clipboard.writeText !== 'function') throw new Error('Clipboard unavailable');
			await navigator.clipboard.writeText(item.id);
			copied = true;
		} catch (error) {
			selectWorkCenterID(cell);
		}
		if (feedback) {
			feedback.textContent = copied ? workCenterText('workCenterIDCopied', 'ID 已复制') : workCenterText('workCenterCopyUnavailable', '无法自动复制，ID 已选中，可按 Ctrl/Cmd+C 复制。');
			feedback.hidden = false;
		}
	}

	function workCenterRowHTML(item) {
		var source = workCenterSourceMeta(item.source);
		var status = workCenterStatus(item.status);
		var selected = item.key === workCenterSelectedKey;
		var row = '<article class="work-center-row ' + (selected ? 'selected' : '') + '" data-work-key="' + escapeAttr(item.key) + '">' +
			'<div class="work-center-row-main" onclick="toggleWorkCenterDetails(' + jsCallArg(item.key) + ')">' +
			'<button class="work-center-task-cell work-center-task-toggle" type="button" onclick="event.stopPropagation();toggleWorkCenterDetails(' + jsCallArg(item.key) + ')" aria-expanded="' + (selected ? 'true' : 'false') + '"><strong>' + escapeHtml(item.title) + '</strong><small>' + escapeHtml(item.description || item.id) + '</small></button>' +
			workCenterIDCellHTML(item) +
			'<span class="work-center-source-cell ' + item.source + '">' + workCenterSourceIcon(item.source) + '<span>' + escapeHtml(source.label) + '</span></span>' +
			'<span class="work-center-owner-cell">' + escapeHtml(item.owner) + '</span>' +
			'<span class="work-center-progress-cell">' + escapeHtml(item.progress || workCenterText('workCenterNoProgress', '暂无进展')) + '</span>' +
			'<span class="work-center-status-cell"><span class="work-center-status ' + status.tone + '">' + escapeHtml(status.label) + '</span></span>' +
			'<span class="work-center-time-cell">' + escapeHtml(workCenterFormatTime(item.updatedAt || item.createdAt)) + '</span>' +
			'<span class="work-center-artifact-cell">' + workCenterArtifactHTML(item, false) + '</span>' +
			'<button class="work-center-action-cell work-center-task-toggle" type="button" onclick="event.stopPropagation();toggleWorkCenterDetails(' + jsCallArg(item.key) + ')" aria-expanded="' + (selected ? 'true' : 'false') + '"><span>' + escapeHtml(workCenterText('workCenterView', '查看')) + '</span><svg viewBox="0 0 20 20" aria-hidden="true"><path d="m7 5 5 5-5 5"/></svg></button>' +
			'</div>';
		if (selected) row += workCenterDetailHTML(item);
		return row + '</article>';
	}

	function workCenterDetailHTML(item) {
		var status = workCenterStatus(item.status);
		var canPause = (item.action === 'task' || item.action === 'pipeline') && (item.status === 'running' || item.status === 'pending');
		var canResume = (item.action === 'task' || item.action === 'pipeline') && item.status === 'paused' && item.canResume !== false;
		var canStop = (item.action === 'task' || item.action === 'pipeline') && ['running', 'pending', 'pausing', 'paused', 'waiting_approval'].indexOf(item.status) !== -1;
		var pauseAction = item.action === 'pipeline' ? 'pausePipelineExecution' : 'pauseTask';
		var resumeAction = item.action === 'pipeline' ? 'resumePipelineExecution' : 'resumeTask';
		var stopAction = item.action === 'pipeline' ? 'stopPipelineExecution' : 'stopTask';
		return '<div class="work-center-detail">' +
			'<div><small>' + escapeHtml(workCenterText('workCenterExecutionInfo', '执行信息')) + '</small><strong>' + escapeHtml(item.owner) + '</strong><span>' + escapeHtml(workCenterSourceMeta(item.source).label) + ' · ' + escapeHtml(item.id || '—') + '</span></div>' +
			'<div><small>' + escapeHtml(workCenterText('workCenterLatestUpdate', '最新更新')) + '</small><strong>' + escapeHtml(status.label) + '</strong><span class="' + (item.error ? 'work-center-detail-error' : '') + '">' + escapeHtml(item.error || item.progress || workCenterText('workCenterNoProgress', '暂无进展')) + '</span></div>' +
			'<div><small>' + escapeHtml(workCenterText('workCenterArtifact', '产物')) + '</small><strong>' + (item.artifacts.length || (item.hasResult ? 1 : 0)) + '</strong><span>' + workCenterArtifactHTML(item, true) + '</span></div>' +
			'<div class="work-center-detail-actions"><button type="button" onclick="openWorkCenterItem(' + jsCallArg(item.key) + ')">' + escapeHtml(workCenterText('workCenterOpenExecution', '查看完整执行')) + '</button>' +
			(canPause ? '<button type="button" onclick="' + pauseAction + '(' + jsCallArg(item.id) + ')">' + escapeHtml(workCenterText('workCenterPause', '暂停')) + '</button>' : '') +
			(canResume ? '<button type="button" onclick="' + resumeAction + '(' + jsCallArg(item.id) + ')">' + escapeHtml(workCenterText('workCenterResume', '继续')) + '</button>' : '') +
			(canStop ? '<button class="danger" type="button" onclick="' + stopAction + '(' + jsCallArg(item.id) + ')">' + escapeHtml(workCenterText('workCenterStop', '停止任务')) + '</button>' : '') + '</div>' +
			'</div>';
	}

	function renderWorkCenter() {
		var container = document.getElementById('taskContent');
		if (!container || workCenterLoading) return;
		var filtered = workCenterFilteredItems();
		var counts = { running: 0, attention: 0, completed: 0 };
		workCenterItems.forEach(function(item) {
			var group = workCenterFilterGroup(item.status);
			if (group === 'running') counts.running++;
			if (group === 'attention') counts.attention++;
			if (String(item.status).toLowerCase() === 'completed') counts.completed++;
		});
		var runningCount = document.getElementById('runningCount');
		var pendingCount = document.getElementById('pendingCount');
		var completedCount = document.getElementById('completedCount');
		var totalCount = document.getElementById('totalCount');
		if (runningCount) runningCount.textContent = counts.running;
		if (pendingCount) pendingCount.textContent = counts.attention;
		if (completedCount) completedCount.textContent = counts.completed;
		if (totalCount) totalCount.textContent = workCenterItems.length;

		var resultCount = document.getElementById('workCenterResultCount');
		if (resultCount) resultCount.textContent = filtered.length + ' ' + workCenterText('workCenterTaskUnit', '个任务');
		container.setAttribute('aria-busy', 'false');
		if (!filtered.length) {
			container.innerHTML = '<div class="work-center-empty"><span>' + workCenterSourceIcon('task') + '</span><strong>' + escapeHtml(workCenterText('workCenterEmptyTitle', '没有符合条件的任务')) + '</strong><p>' + escapeHtml(workCenterText('workCenterEmptyHint', '试试切换状态、来源或清除搜索条件。')) + '</p></div>';
			return;
		}
		container.innerHTML = filtered.map(workCenterRowHTML).join('');
	}

	function toggleWorkCenterDetails(key) {
		workCenterSelectedKey = workCenterSelectedKey === key ? '' : key;
		renderWorkCenter();
	}

	function openWorkCenterItem(key) {
		var item = workCenterItems.find(function(candidate) { return candidate.key === key; });
		if (!item) return;
		closeTaskPanel();
		if (item.action === 'pipeline' && typeof showExecutionDetail === 'function') {
			showExecutionDetail(item.id, item.owner, item.title);
			return;
		}
		if (item.action === 'employee' && typeof openAEPanel === 'function') {
			openAEPanel();
			return;
		}
		if (item.action === 'scheduled' && typeof openTodoPanel === 'function') {
			openTodoPanel();
			return;
		}
		if (typeof openTrace === 'function') openTrace(item.id);
	}

	async function loadTasks(showFeedback, silent) {
		if (workCenterLoading) return;
		workCenterLoading = true;
		var container = document.getElementById('taskContent');
		var refresh = document.querySelector('#taskModal .work-center-refresh');
		if (refresh) refresh.classList.add('loading');
		if (container && !silent) {
			container.setAttribute('aria-busy', 'true');
			container.innerHTML = '<div class="work-center-loading"><span></span><span></span><span></span></div>';
		}
		try {
			var results = await Promise.all([
				fetchWorkCenterJSON('/api/tasks/running?include_history=true&limit=100'),
				fetchWorkCenterJSON('/api/tasks?limit=100'),
				fetchWorkCenterJSON('/api/pipeline/executions'),
				fetchWorkCenterJSON('/api/ae/worklog'),
				fetchWorkCenterJSON('/api/todo/list?limit=100')
			]);
			var successful = results.filter(function(result) { return result.ok; }).length;
			if (!successful) throw new Error(workCenterText('workCenterLoadFailed', '暂时无法读取任务数据'));
			workCenterItems = buildWorkCenterItems(
				results[0].data || {}, results[1].data || {}, results[2].data || {}, results[3].data || {}, results[4].data || {}
			);
			workCenterLastRefresh = new Date();
			if (workCenterSelectedKey && !workCenterItems.some(function(item) { return item.key === workCenterSelectedKey; })) workCenterSelectedKey = '';
			workCenterLoading = false;
			renderWorkCenter();
			var updatedAt = document.getElementById('workCenterUpdatedAt');
			if (updatedAt) updatedAt.textContent = workCenterText('workCenterLastRefresh', '最后刷新：') + workCenterFormatTime(workCenterLastRefresh);
			if (showFeedback && refresh) {
				var refreshLabel = refresh.querySelector('span');
				if (refreshLabel) refreshLabel.textContent = workCenterText('workCenterRefreshDone', '已刷新');
				refresh.classList.add('refreshed');
				setTimeout(function() {
					refresh.classList.remove('refreshed');
					if (refreshLabel) refreshLabel.textContent = workCenterText('workCenterRefresh', '刷新');
				}, 1200);
			}
		} catch (err) {
			workCenterLoading = false;
			console.error('加载任务中心失败:', err);
			if (container) {
				container.setAttribute('aria-busy', 'false');
				container.innerHTML = '<div class="work-center-empty error"><strong>' + escapeHtml(workCenterText('workCenterLoadFailed', '暂时无法读取任务数据')) + '</strong><p>' + escapeHtml(err.message || '') + '</p><button type="button" onclick="loadTasks(true)">' + escapeHtml(workCenterText('workCenterRetry', '重新加载')) + '</button></div>';
			}
		} finally {
			if (refresh) refresh.classList.remove('loading');
		}
	}

	document.addEventListener('openkitty:languagechange', function() {
		var modal = document.getElementById('taskModal');
		if (modal && modal.classList.contains('open')) renderWorkCenter();
	});

	async function pauseTask(taskId) {
		try {
			var response = await fetch('/api/task/pause/' + encodeURIComponent(taskId), { method: 'POST' });
			var result = await response.json().catch(function() { return {}; });
			if (!response.ok) throw new Error(result.error || ('HTTP ' + response.status));
			if (typeof showToast === 'function') showToast(workCenterText('workCenterPauseRequested', '暂停请求已提交'), 'success');
			loadTasks(false, true);
		} catch (err) {
			if (typeof showToast === 'function') showToast(workCenterText('workCenterPauseFailed', '暂停失败') + ': ' + err.message, 'error');
			else alert(err.message);
		}
	}

	async function resumeTask(taskId) {
		try {
			var response = await fetch('/api/task/resume', {
				method: 'POST',
				headers: { 'Content-Type': 'application/json; charset=utf-8', Accept: 'application/json' },
				body: JSON.stringify({ task_id: taskId })
			});
			var result = await response.json().catch(function() { return {}; });
			if (!response.ok) throw new Error(result.error || ('HTTP ' + response.status));
			if (typeof showToast === 'function') showToast(workCenterText('workCenterResumeStarted', '任务已从断点继续'), 'success');
			loadTasks(false, true);
		} catch (err) {
			if (typeof showToast === 'function') showToast(workCenterText('workCenterResumeFailed', '继续失败') + ': ' + err.message, 'error');
			else alert(err.message);
		}
	}

	async function pipelineControl(action, execId) {
		var response = await fetch('/api/pipeline/' + action, {
			method: 'POST',
			headers: { 'Content-Type': 'application/json; charset=utf-8', Accept: 'application/json' },
			body: JSON.stringify({ exec_id: execId })
		});
		var result = await response.json().catch(function() { return {}; });
		if (!response.ok) throw new Error(result.error || ('HTTP ' + response.status));
		return result;
	}

	async function pausePipelineExecution(execId) {
		try {
			await pipelineControl('pause', execId);
			if (typeof showToast === 'function') showToast(workCenterText('workCenterPipelinePauseRequested', '工作流将在当前步骤结束后暂停'), 'success');
			loadTasks(false, true);
		} catch (err) {
			if (typeof showToast === 'function') showToast(workCenterText('workCenterPauseFailed', '暂停失败') + ': ' + err.message, 'error');
		}
	}

	async function resumePipelineExecution(execId) {
		try {
			await pipelineControl('resume', execId);
			if (typeof showToast === 'function') showToast(workCenterText('workCenterResumeStarted', '任务已从断点继续'), 'success');
			loadTasks(false, true);
		} catch (err) {
			if (typeof showToast === 'function') showToast(workCenterText('workCenterResumeFailed', '继续失败') + ': ' + err.message, 'error');
		}
	}

	async function stopPipelineExecution(execId) {
		if (!confirm(workCenterText('workCenterStopConfirm', '确定停止此任务吗？'))) return;
		try {
			await pipelineControl('stop', execId);
			if (typeof showToast === 'function') showToast(workCenterText('workCenterStopped', '任务已停止'), 'success');
			loadTasks(false, true);
		} catch (err) {
			if (typeof showToast === 'function') showToast(workCenterText('workCenterStopFailed', '停止失败') + ': ' + err.message, 'error');
		}
	}

	window.pauseTask = pauseTask;
	window.resumeTask = resumeTask;
	window.pausePipelineExecution = pausePipelineExecution;
	window.resumePipelineExecution = resumePipelineExecution;
	window.stopPipelineExecution = stopPipelineExecution;



	async function stopTask(taskId) {

		if (!confirm('确定要停止任务 ' + taskId.substring(0, 30) + '... 吗？')) {

			return;

		}

		try {

			addLog('info', '正在停止任务: ' + taskId);

			var response = await fetch('/api/task/stop/' + taskId, {

				method: 'POST',

				headers: { 'Content-Type': 'application/json' }

			});

			if (response.ok) {

				var result = await response.json();

				addLog('info', '任务已停止: ' + taskId);

				alert('✅ 任务已停止');

				loadTasks();

			} else {

				var errorText = await response.text();

				addLog('error', '停止任务失败: ' + errorText);

				alert('停止失败: ' + errorText);

			}

		} catch (err) {

			addLog('error', '停止任务失败: ' + err.message);

			alert('停止失败: ' + err.message);

		}

	}



	async function batchStopTasks() {

		var taskIds = [];

		var taskItems = document.querySelectorAll('.task-item.running, .task-item.pending');

		for (var i = 0; i < taskItems.length; i++) {

			var idElement = taskItems[i].querySelector('.task-id');

			if (idElement) {

				var idText = idElement.textContent;

				var id = idText.replace('ID: ', '').replace('...', '');

				taskIds.push(id);

			}

		}

		if (taskIds.length === 0) {

			alert('没有运行中或等待中的任务');

			return;

		}

		if (!confirm('确定要停止 ' + taskIds.length + ' 个任务吗？')) {

			return;

		}

		try {

			var response = await fetch('/api/tasks/stop/batch', {

				method: 'POST',

				headers: { 'Content-Type': 'application/json' },

				body: JSON.stringify({ task_ids: taskIds })

			});

			if (response.ok) {

				var result = await response.json();

				addLog('info', '批量停止任务完成: ' + result.results.length + ' 个任务');

				alert('✅ 已停止 ' + result.results.length + ' 个任务');

				loadTasks();

			} else {

				alert('批量停止失败');

			}

		} catch (err) {

			alert('批量停止失败: ' + err.message);

		}

	}



	// 自动刷新任务列表（每5秒）

	let taskRefreshInterval = null;



	function startTaskAutoRefresh() {

		if (taskRefreshInterval) clearInterval(taskRefreshInterval);

		taskRefreshInterval = setInterval(() => {

			var tp = document.getElementById('taskPanel');
		if (tp && tp.classList.contains('open')) {

				loadTasks();

			}

		}, 5000);

	}



	function stopTaskAutoRefresh() {

		if (taskRefreshInterval) {

			clearInterval(taskRefreshInterval);

			taskRefreshInterval = null;

		}

	}



	// 在打开任务面板时启动自动刷新

	const originalOpenTaskPanel = openTaskPanel;

	openTaskPanel = function() {

		originalOpenTaskPanel();

		startTaskAutoRefresh();
checkForUpdate();

	};



	const originalCloseTaskPanel = closeTaskPanel;

	closeTaskPanel = function() {

		originalCloseTaskPanel();

		stopTaskAutoRefresh();

	};



	function updateInputAreaState() {

        const editable = isSessionEditable();

        const input = document.getElementById('messageInput');

        const sendBtn = document.getElementById('sendBtn');

        if (!editable) {

            input.disabled = true;

            input.placeholder = '此会话来自其他渠道，仅可查看历史消息';

            sendBtn.disabled = true;

        } else {

            input.disabled = false;

            input.placeholder = '输入消息... (Shift+Enter 换行，Enter 发送)';

            sendBtn.disabled = false;

        }

        window.syncPromptComposerSession?.();

    }





	    // Cache the original about tab HTML so it survives innerHTML replacements
	    var _aboutTabHTML = document.getElementById('todoTab-about') ? document.getElementById('todoTab-about').innerHTML : '';









	// 转换用户友好的配置为Cron表达式




	// 转换间隔配置为秒数





	// createTodo 函数





	    // Helper: parse cron expr back to friendly UI values



    // 文件预览功能

   async function previewFile(fileName) {

		var filePath = currentFilePath === '/' ? fileName : currentFilePath + '/' + fileName;

		var ext = fileName.split('.').pop().toLowerCase();

		var modal = document.getElementById('previewModal');

		var previewBody = document.getElementById('previewBody');

		var previewTitle = document.getElementById('previewTitle');

		previewTitle.textContent = '预览: ' + fileName;

		previewBody.innerHTML = '<div style="text-align:center; padding:40px;">📄 加载中...</div>';

		modal.classList.remove('image-preview-active');
		modal.classList.add('active');

		try {

			var downloadUrl = '/api/files/download?path=' + encodeURIComponent(filePath);

			// 代码文件扩展名列表

			var codeExtensions = ['py', 'go', 'c', 'cpp', 'cc', 'cxx', 'h', 'hpp', 'java', 'js', 'ts', 'jsx', 'tsx', 

									'html', 'htm', 'css', 'scss', 'sass', 'less', 'json', 'yaml', 'yml', 'xml', 

									'sh', 'bash', 'zsh', 'ps1', 'sql', 'r', 'rb', 'php', 'swift', 'kt', 'scala',

									'rs', 'dart', 'lua', 'pl', 'pm', 'tcl', 'makefile', 'dockerfile', 'ini', 'cfg', 'conf'];

			// 图片扩展名

			var imageExtensions = ['jpg', 'jpeg', 'png', 'gif', 'svg', 'webp', 'bmp', 'ico'];

			// Markdown

			if (ext === 'md' || ext === 'markdown') {

				var resp = await fetch(downloadUrl);

				var text = await resp.text();

				var html = marked.parse(text);

				previewBody.innerHTML = '<div class="markdown-preview" style="padding: 20px;">' + html + '</div>';

				if (typeof hljs !== 'undefined') {

					document.querySelectorAll('pre code').forEach(function(block) { hljs.highlightElement(block); });

				}

			}

			// PDF

			else if (ext === 'pdf') {

				previewBody.innerHTML = 

					'<div style="display:flex; flex-direction:column; align-items:center;">' +

						'<canvas id="pdfCanvas" style="max-width:100%; height:auto;"></canvas>' +

						'<div id="pdfControls" style="margin-top:15px; display:flex; gap:10px; align-items:center;">' +

							'<button onclick="prevPage()" style="padding:5px 12px; cursor:pointer;">◀ 上一页</button>' +

							'<span id="pageNum">1</span> / <span id="pageCount">0</span>' +

							'<button onclick="nextPage()" style="padding:5px 12px; cursor:pointer;">下一页 ▶</button>' +

							'<input type="number" id="pageInput" style="width:50px;" placeholder="页数">' +

							'<button onclick="goToPage()">跳转</button>' +

						'</div>' +

					'</div>';

				var loadingTask = pdfjsLib.getDocument(downloadUrl);

				var pdf = await loadingTask.promise;

				window.currentPdf = pdf;

				window.currentPage = 1;

				document.getElementById('pageCount').textContent = pdf.numPages;

				renderPage(1);

				// 添加跳转功能

				var pageInput = document.getElementById('pageInput');

				if (pageInput) {

					pageInput.addEventListener('keypress', function(e) {

						if (e.key === 'Enter') goToPage();

					});

				}

			}

			// 代码文件

			else if (codeExtensions.includes(ext)) {

				var resp = await fetch(downloadUrl);

				var text = await resp.text();

				// 获取语言标识

				var langMap = {

					'py': 'python', 'go': 'go', 'c': 'c', 'cpp': 'cpp', 'cc': 'cpp', 'cxx': 'cpp',

					'h': 'c', 'hpp': 'cpp', 'java': 'java', 'js': 'javascript', 'ts': 'typescript',

					'jsx': 'javascript', 'tsx': 'typescript', 'html': 'html', 'htm': 'html',

					'css': 'css', 'scss': 'scss', 'sass': 'sass', 'less': 'less', 'json': 'json',

					'yaml': 'yaml', 'yml': 'yaml', 'xml': 'xml', 'sh': 'bash', 'bash': 'bash',

					'zsh': 'bash', 'ps1': 'powershell', 'sql': 'sql', 'r': 'r', 'rb': 'ruby',

					'php': 'php', 'swift': 'swift', 'kt': 'kotlin', 'scala': 'scala', 'rs': 'rust',

					'dart': 'dart', 'lua': 'lua', 'makefile': 'makefile', 'dockerfile': 'dockerfile'

				};

				var language = langMap[ext] || 'plaintext';

				// 添加行号

				var lines = text.split('\n');

				var lineNumbers = '';

				for (var i = 0; i < lines.length; i++) {

					lineNumbers += (i + 1) + '\n';

				}

				previewBody.innerHTML = 

					'<div style="display:flex; gap:0; border-radius:8px; overflow:hidden; border:1px solid #e1e4e8;">' +

						'<div class="line-numbers" style="background:#f6f8fa; padding:12px 8px; text-align:right; color:#6a737d; font-family:Monaco,Menlo,monospace; font-size:calc(14.3px * var(--macs-font-scale, 1)); line-height:1.5; user-select:none; border-right:1px solid #e1e4e8;">' +

							'<pre style="margin:0; padding:0;">' + lineNumbers + '</pre>' +

						'</div>' +

						'<div style="flex:1; overflow-x:auto;">' +

							'<pre style="margin:0; padding:12px; background:#f6f8fa; font-family:Monaco,Menlo,monospace; font-size:calc(14.3px * var(--macs-font-scale, 1)); line-height:1.5;"><code class="language-' + language + '" style="background:transparent; padding:0;">' + escapeHtml(text) + '</code></pre>' +

						'</div>' +

					'</div>';

				// 应用语法高亮

				if (typeof hljs !== 'undefined') {

					var codeBlock = previewBody.querySelector('code');

					if (codeBlock) {

						hljs.highlightElement(codeBlock);

					}

				}

				// 添加文件信息栏

				var infoBar = document.createElement('div');

				infoBar.style.cssText = 'margin-bottom:12px; padding:8px 12px; background:#eef2f5; border-radius:8px; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#586069; display:flex; justify-content:space-between;';

				infoBar.innerHTML = 

					'<span>📄 文件类型: ' + ext.toUpperCase() + '</span>' +

					'<span>📏 行数: ' + lines.length + '</span>' +

					'<span>📊 大小: ' + formatFileSize(text.length) + '</span>' +

					'<span>🔤 编码: UTF-8</span>';

				previewBody.insertBefore(infoBar, previewBody.firstChild);

			}

			// DOCX/Word 文件（使用 mammoth.js 转 HTML 预览）

			else if (ext === 'docx' || ext === 'doc') {

				var resp = await fetch(downloadUrl);

				var blob = await resp.blob();

				try {

					var arrayBuffer = await blob.arrayBuffer();

					var result = await mammoth.convertToHtml({arrayBuffer: arrayBuffer});

					previewBody.innerHTML =

						'<div class="markdown-preview" style="padding:20px; max-height:70vh; overflow-y:auto;">' +

							result.value +

						'</div>';

					if (result.messages && result.messages.length) {

						console.log('Mammoth warnings:', result.messages);

					}

				} catch (mammothErr) {

					previewBody.innerHTML =

						'<div style="text-align:center; padding:30px; position:relative;">' +

							'<div style="font-size:calc(52.8px * var(--macs-font-scale, 1)); margin-bottom:12px;">📝</div>' +

							'<div style="font-size:calc(17.6px * var(--macs-font-scale, 1)); color:#586069; margin-bottom:8px;">' + escapeHtml(fileName) + '</div>' +

							'<div style="font-size:calc(14.3px * var(--macs-font-scale, 1)); color:#6a737d; margin-bottom:16px;">Word 文档 (大小: ' + formatFileSize(blob.size) + ')</div>' +

							'<a href="' + downloadUrl + '" download="' + fileName + '" style="position:absolute;right:30px;bottom:8px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#2563eb;text-decoration:none;">⬇ 下载</a>' +

						'</div>';

				}

			}

			// PPT/PPTX 文件（提供下载预览）

			else if (ext === 'pptx' || ext === 'ppt') {

				var resp = await fetch(downloadUrl);

				var blob = await resp.blob();

				previewBody.innerHTML =

					'<div style="text-align:center; padding:40px 20px;">' +

						'<div style="font-size:calc(70.4px * var(--macs-font-scale, 1)); margin-bottom:16px;">📊</div>' +

						'<div style="font-size:calc(19.8px * var(--macs-font-scale, 1)); font-weight:600; color:#333; margin-bottom:8px;">' + escapeHtml(fileName) + '</div>' +

						'<div style="font-size:calc(15.4px * var(--macs-font-scale, 1)); color:#586069; margin-bottom:4px;">PowerPoint 演示文稿</div>' +

						'<div style="font-size:calc(14.3px * var(--macs-font-scale, 1)); color:#6a737d; margin-bottom:20px;">大小: ' + formatFileSize(blob.size) + '</div>' +

						'<div style="display:flex; gap:10px; justify-content:center; align-items:center;">' +

							'<a href="' + downloadUrl + '" download="' + fileName + '" style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#2563eb;text-decoration:none;">⬇ 下载</a>' +

							'<button onclick="window.open(\'' + downloadUrl + '\')" style="padding:6px 16px; background:#2563eb; color:white; border:none; border-radius:6px; cursor:pointer; font-size:calc(13.2px * var(--macs-font-scale, 1));">🔗 新窗口打开</button>' +

						'</div>' +

						'<div style="font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#adb5bd; margin-top:16px;">PPTX 预览需要下载到本地后用 PowerPoint/WPS 打开</div>' +

					'</div>';

			}

			// 图片文件

			else if (imageExtensions.includes(ext)) {

				previewBody.innerHTML = 

					'<div style="display:flex; justify-content:center; align-items:center; min-height:400px;">' +

						'<img src="' + downloadUrl + '" style="max-width:100%; max-height:70vh; border-radius:8px; box-shadow:0 4px 12px rgba(0,0,0,0.15);" alt="' + fileName + '">' +

					'</div>';

			}

			// 文本文件

			else if (ext === 'txt' || ext === 'log' || ext === 'csv') {

				var resp = await fetch(downloadUrl);

				var text = await resp.text();

				previewBody.innerHTML = 

					'<div style="background:#f6f8fa; border-radius:8px; padding:16px; overflow-x:auto;">' +

						'<pre style="margin:0; white-space:pre-wrap; font-family:Monaco,Menlo,monospace; font-size:calc(14.3px * var(--macs-font-scale, 1)); line-height:1.5;">' + escapeHtml(text) + '</pre>' +

					'</div>';

			}

			// 不支持预览的文件类型

			else {

				previewBody.innerHTML = 

					'<div style="text-align:center; padding:60px 20px; position:relative;">' +

						'<div style="font-size:calc(52.8px * var(--macs-font-scale, 1)); margin-bottom:16px;">📄</div>' +

						'<div style="font-size:calc(17.6px * var(--macs-font-scale, 1)); color:#586069; margin-bottom:12px;">不支持预览 .' + ext + ' 文件</div>' +

						'<div style="font-size:calc(15.4px * var(--macs-font-scale, 1)); color:#6a737d;">支持预览: 代码 / Markdown / PDF / DOCX / PPTX / 图片 / 文本</div>' +

						'<a href="' + downloadUrl + '" download="' + fileName + '" style="position:absolute;right:20px;bottom:10px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#2563eb;text-decoration:none;">⬇ 下载</a>' +

					'</div>';

			}

		} catch (err) {

			console.error('预览失败:', err);

			previewBody.innerHTML = 

				'<div style="text-align:center; padding:40px; color:#dc3545;">' +

					'<div style="font-size:calc(52.8px * var(--macs-font-scale, 1)); margin-bottom:16px;">❌</div>' +

					'<div>预览失败: ' + escapeHtml(err.message) + '</div>' +

					'<div style="margin-top:16px; font-size:calc(14.3px * var(--macs-font-scale, 1)); color:#6c757d;">可能是文件过大或格式不支持</div>' +

				'</div>';

		}

	}



    function goToPage() {

		if (!window.currentPdf) return;

		const pageInput = document.getElementById('pageInput');

		const pageNum = parseInt(pageInput.value);

		if (pageNum && pageNum >= 1 && pageNum <= window.currentPdf.numPages) {

			window.currentPage = pageNum;

			renderPage(pageNum);

			pageInput.value = '';

		}

	}



	var codeStyles = document.createElement('style');

	codeStyles.textContent = 

			'/* 代码高亮主题 - GitHub 风格 */' +

			'.hljs {' +

			'    background: #f6f8fa !important;' +

			'    color: #24292e !important;' +

			'}' +

			'.hljs-keyword,' +

			'.hljs-selector-tag,' +

			'.hljs-subst {' +

			'    color: #d73a49 !important;' +

			'}' +

			'.hljs-string,' +

			'.hljs-doctag {' +

			'    color: #032f62 !important;' +

			'}' +

			'.hljs-number,' +

			'.hljs-literal {' +

			'    color: #005cc5 !important;' +

			'}' +

			'.hljs-comment,' +

			'.hljs-quote {' +

			'    color: #6a737d !important;' +

			'    font-style: italic;' +

			'}' +

			'.hljs-function,' +

			'.hljs-title {' +

			'    color: #6f42c1 !important;' +

			'}' +

			'.hljs-built_in,' +

			'.hljs-builtin-name {' +

			'    color: #e36209 !important;' +

			'}' +

			'.hljs-variable {' +

			'    color: #e36209 !important;' +

			'}' +

			'.hljs-attr {' +

			'    color: #005cc5 !important;' +

			'}' +

			'.hljs-tag {' +

			'    color: #22863a !important;' +

			'}' +

			'.hljs-name {' +

			'    color: #22863a !important;' +

			'}' +

			'.hljs-attribute {' +

			'    color: #005cc5 !important;' +

			'}' +

			'/* 行号样式 */' +

			'.line-numbers pre {' +

			'    font-family: Monaco, Menlo, Consolas, monospace;' +

			'    font-size: calc(14.3px * var(--macs-font-scale, 1));' +

			'    line-height: 1.5;' +

			'}' +

			'/* 代码块滚动条 */' +

			'.preview-body pre::-webkit-scrollbar {' +

			'    height: 8px;' +

			'    width: 8px;' +

			'}' +

			'.preview-body pre::-webkit-scrollbar-track {' +

			'    background: #f1f1f1;' +

			'    border-radius: 4px;' +

			'}' +

			'.preview-body pre::-webkit-scrollbar-thumb {' +

			'    background: #c1c1c1;' +

			'    border-radius: 4px;' +

			'}' +

			'.preview-body pre::-webkit-scrollbar-thumb:hover {' +

			'    background: #a8a8a8;' +

			'}' +

			'/* Markdown 预览样式 */' +

			'.markdown-preview {' +

			'    line-height: 1.6;' +

			'}' +

			'.markdown-preview h1,' +

			'.markdown-preview h2,' +

			'.markdown-preview h3 {' +

			'    border-bottom: 1px solid #eaecef;' +

			'    padding-bottom: 0.3em;' +

			'}' +

			'.markdown-preview code {' +

			'    background: #f6f8fa;' +

			'    padding: 0.2em 0.4em;' +

			'    border-radius: 3px;' +

			'    font-family: monospace;' +

			'}' +

			'.markdown-preview pre {' +

			'    background: #f6f8fa;' +

			'    padding: 16px;' +

			'    border-radius: 6px;' +

			'    overflow-x: auto;' +

			'}' +

			'.markdown-preview table {' +

			'    border-collapse: collapse;' +

			'    width: 100%;' +

			'}' +

			'.markdown-preview th,' +

			'.markdown-preview td {' +

			'    border: 1px solid #dfe2e5;' +

			'    padding: 6px 13px;' +

			'}' +

			'.markdown-preview blockquote {' +

			'    border-left: 4px solid #dfe2e5;' +

			'    padding-left: 16px;' +

			'    color: #6a737d;' +

			'}';

	document.head.appendChild(codeStyles);



    function closePreview() {

        var modal = document.getElementById('previewModal');
        if (modal) {
            modal.classList.remove('active');
            modal.classList.remove('image-preview-active');
        }

        window.currentPdf = null;

    }

    async function renderPage(pageNum) {

        if (!window.currentPdf) return;

        const page = await window.currentPdf.getPage(pageNum);

        const canvas = document.getElementById('pdfCanvas');

        const context = canvas.getContext('2d');

        const viewport = page.getViewport({ scale: 1.5 });

        canvas.height = viewport.height;

        canvas.width = viewport.width;

        await page.render({

            canvasContext: context,

            viewport: viewport

        }).promise;

        document.getElementById('pageNum').textContent = pageNum;

    }

    function prevPage() {

        if (window.currentPdf && window.currentPage > 1) {

            window.currentPage--;

            renderPage(window.currentPage);

        }

    }

    function nextPage() {

        if (window.currentPdf && window.currentPage < window.currentPdf.numPages) {

            window.currentPage++;

            renderPage(window.currentPage);

        }

    }

	function isSessionEditable() {

        if (!currentSessionId) return true;

        // 其他渠道的会话只读
        var externalPrefixes = ['qq_', 'feishu_', 'dingtalk_', 'wework_', 'email_', 'telegram_'];
        for (var i = 0; i < externalPrefixes.length; i++) {
            if (currentSessionId.startsWith(externalPrefixes[i])) return false;
        }

        // web 会话及其他都允许输入
        return true;

    }



	function getUserId() {

		let userId = localStorage.getItem('userId');

		if (!userId) {

			userId = 'web' ;

			localStorage.setItem('userId', userId);

		}

		return userId;

	}





	async function openFileModal() {

		document.getElementById('fileModal').classList.add('active');

		currentFilePath = '/.openkitty/storage';

		await loadFileList('/.openkitty/storage');

	}



	// 关闭文件管理模态框

	function closeFileModal() {

		document.getElementById('fileModal').classList.remove('active');

	}



	async function loadFileList(path) {

		try {

			addLog('info', '加载文件列表: ' + path);

			var response = await fetch('/api/files/list?path=' + encodeURIComponent(path));

			if (!response.ok) {

				throw new Error('HTTP ' + response.status);

			}

			var data = await response.json();

			currentFileList = data.files || [];

			filteredFileList = [...currentFileList];

			// 更新当前路径显示

			document.getElementById('currentPath').textContent = path;

			// 显示/隐藏上级目录按钮

			var parentBtn = document.getElementById('parentBtn');

			if (path === '/' || path === '') {

				parentBtn.style.display = 'none';

			} else {

				parentBtn.style.display = 'inline-block';

			}

			renderFileList();

			addLog('info', '加载了 ' + currentFileList.length + ' 个文件/目录');

		} catch (err) {

			console.error('加载文件列表失败:', err);

			addLog('error', '加载文件列表失败: ' + err.message);

			document.getElementById('fileList').innerHTML = 

				'<div style="text-align: center; padding: 40px; color: #f48771;">❌ 加载失败: ' + escapeHtml(err.message) + '</div>';

		}

	}



	// 渲染文件列表

	function renderFileList() {

        var container = document.getElementById('fileList');

        if (filteredFileList.length === 0) {

            container.innerHTML = '<div style="text-align: center; padding: 40px; color: #adb5bd;"> 没有文件</div>';

            return;

        }

        var html = '';

        for (var i = 0; i < filteredFileList.length; i++) {

            var file = filteredFileList[i];

            var isDir = file.is_dir;

            var icon = isDir ? '<svg viewBox="0 0 24 24" width="18" height="18" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="color:#6366f1;"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>' : getFileIcon(file.name);

            var sizeText = isDir ? '-' : formatFileSize(file.size);

            var timeText = file.mod_time ? formatDate(file.mod_time) : '-';

            html += '<div class="file-item' + (isDir ? ' directory' : '') + '">' +

                '<div class="file-info" onclick="' + (isDir ? 'enterDirectory(\'' + escapeHtml(file.name) + '\')' : 'previewFile(\'' + escapeHtml(file.name) + '\')') + '">' +

                '<div class="file-icon">' + icon + '</div>' +

                '<div class="file-name">' + escapeHtml(file.name) + '</div>' +

                '</div>' +

                '<div class="file-meta">' +

                '<div class="file-size">' + sizeText + '</div>' +

                '<div class="file-time">' + timeText + '</div>' +

                (!isDir ? '<button class="download-btn" onclick="event.stopPropagation(); downloadFile(\'' + escapeHtml(file.name) + '\')">⬇ 下载</button>' : '') +

                (!isDir ? '<button class="preview-btn" onclick="event.stopPropagation(); previewFile(\'' + escapeHtml(file.name) + '\')">👁️ 预览</button>' : '') +

                '</div>' +

                '</div>';

        }

        container.innerHTML = html;

    }



	// 进入目录

	async function enterDirectory(dirName) {

		var newPath = currentFilePath === '/' ? '/' + dirName : currentFilePath + '/' + dirName;

		newPath = newPath.replace(/\/+/g, '/'); // 清理重复斜杠

		currentFilePath = newPath;

		await loadFileList(currentFilePath);

	}



	// 返回上级目录

	async function goToParent() {

		if (currentFilePath === '/' || currentFilePath === '') return;

		var parts = currentFilePath.split('/').filter(function(p) { return p; });

		parts.pop();

		var newPath = parts.length === 0 ? '/' : '/' + parts.join('/');

		currentFilePath = newPath;

		await loadFileList(currentFilePath);

	}



	// 下载文件

	async function downloadFile(fileName) {

		try {

			var filePath = currentFilePath === '/' ? fileName : currentFilePath + '/' + fileName;

			filePath = filePath.replace(/\/+/g, '/');

			addLog('info', '开始下载文件: ' + filePath);

			// 显示下载进度

			var progressDiv = document.getElementById('downloadProgress');

			var progressFill = document.getElementById('downloadProgressFill');

			var statusDiv = document.getElementById('downloadStatus');

			progressDiv.style.display = 'block';

			progressFill.style.width = '0%';

			statusDiv.innerHTML = '⬇ 正在下载: ' + escapeHtml(fileName) + '...';

			// 创建下载链接

			var downloadUrl = '/api/files/download?path=' + encodeURIComponent(filePath);

			// 模拟进度（实际进度需要后端支持）

			var progress = 0;

			var interval = setInterval(function() {

				progress += 10;

				if (progress <= 90) {

					progressFill.style.width = progress + '%';

				}

				if (progress >= 100) {

					clearInterval(interval);

				}

			}, 200);

			// 创建隐藏的 a 标签触发下载

			var a = document.createElement('a');

			a.href = downloadUrl;

			a.download = fileName;

			document.body.appendChild(a);

			a.click();

			document.body.removeChild(a);

			// 延迟隐藏进度条

			setTimeout(function() {

				clearInterval(interval);

				progressFill.style.width = '100%';

				statusDiv.innerHTML = '✅ 下载完成: ' + escapeHtml(fileName);

				setTimeout(function() {

					progressDiv.style.display = 'none';

					statusDiv.innerHTML = '';

				}, 2000);

			}, 2000);

			addLog('info', '文件下载完成: ' + filePath);

		} catch (err) {

			console.error('下载失败:', err);

			addLog('error', '下载失败: ' + err.message);

			alert('下载失败: ' + err.message);

			var progressDiv = document.getElementById('downloadProgress');

			progressDiv.style.display = 'none';

		}

	}



	// 刷新文件列表

	async function refreshFileList() {

		await loadFileList(currentFilePath);

		addLog('info', '刷新文件列表');

	}



	function initPresetButtons() {

		const presetBtns = document.querySelectorAll('.preset-btn');

		presetBtns.forEach(btn => {

			btn.addEventListener('click', function(e) {

				e.preventDefault();

				const message = this.getAttribute('data-message');

				if (message) {

					// 将预设消息填入输入框

					const input = document.getElementById('messageInput');

					input.value = message;

					// 可选：自动聚焦到输入框

					input.focus();

					// 可选：自动发送（如果需要自动发送，取消下面的注释）

					// setTimeout(() => sendMessage(), 100);

					addLog('info', '使用预设消息: ' + message);

					// 添加视觉反馈

					closeGeneralPresetMenu();
					closeCommonQuestions();

					this.style.transform = 'scale(0.95)';

					setTimeout(() => {

						this.style.transform = '';

					}, 150);

				}

			});

		});

	}





	// 过滤文件列表

	function toggleGeneralPresetMenu(e) {
		if (e) {
			e.preventDefault();
			e.stopPropagation();
		}
		var menu = document.getElementById('generalPresetMenu');
		if (!menu) return;
		menu.style.display = menu.style.display === 'none' || menu.style.display === '' ? 'block' : 'none';
	}

	function setCommonQuestionsOpen(open) {
		var panel = document.getElementById('commonQuestionsPanel');
		var button = document.getElementById('commonQuestionsButton');
		if (!panel || !button) return;
		panel.hidden = !open;
		button.setAttribute('aria-expanded', open ? 'true' : 'false');
		button.classList.toggle('active', open);
		if (open) {
			var first = panel.querySelector('.common-question-item');
			if (first) setTimeout(function() { first.focus(); }, 0);
		}
	}

	function toggleCommonQuestions(e) {
		if (e) {
			e.preventDefault();
			e.stopPropagation();
		}
		closeSidebarTools();
		var panel = document.getElementById('commonQuestionsPanel');
		setCommonQuestionsOpen(!panel || panel.hidden);
	}

	function closeCommonQuestions() {
		setCommonQuestionsOpen(false);
	}

	window.toggleCommonQuestions = toggleCommonQuestions;
	window.closeCommonQuestions = closeCommonQuestions;

	function closeGeneralPresetMenu() {
		var menu = document.getElementById('generalPresetMenu');
		if (menu) menu.style.display = 'none';
	}

	document.addEventListener('click', function(e) {
		var commonPanel = document.getElementById('commonQuestionsPanel');
		var commonButton = document.getElementById('commonQuestionsButton');
		if (commonPanel && !commonPanel.hidden && !commonPanel.contains(e.target) && (!commonButton || !commonButton.contains(e.target))) {
			closeCommonQuestions();
		}
		var menu = document.getElementById('generalPresetMenu');
		if (!menu || menu.style.display === 'none') return;
		var dropdown = menu.closest('.preset-dropdown');
		if (dropdown && dropdown.contains(e.target)) return;
		closeGeneralPresetMenu();
	});

	document.addEventListener('keydown', function(e) {
		if (e.key === 'Escape') closeCommonQuestions();
	});

	function filterFileList() {

		var typeFilter = document.getElementById('fileTypeFilter').value;

		var searchText = document.getElementById('fileSearch').value.toLowerCase();

		filteredFileList = currentFileList.filter(function(file) {

			// 类型过滤

			if (typeFilter === 'file' && file.is_dir) return false;

			if (typeFilter === 'directory' && !file.is_dir) return false;

			// 搜索过滤

			if (searchText && file.name.toLowerCase().indexOf(searchText) === -1) return false;

			return true;

		});

		renderFileList();

	}



	// 获取文件图标

	function getFileIcon(fileName) {
		var ext = fileName.split('.').pop().toLowerCase();
		var a = "width='18' height='18' stroke='currentColor' fill='none' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'";
		var s = function(p,c){ return "<svg viewBox=\"0 0 24 24\" "+a+" style=\"color:"+c+";\">"+p+"</svg>"; };
		var map = {
			"txt": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/>', "#6b7280"),
			"md": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><polyline points="8 17 12 14 16 17"/>', "#3b82f6"),
			"json": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><path d="M10 12l-2 2 2 2"/><path d="M14 12l2 2-2 2"/>', "#f59e0b"),
			"js": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>', "#eab308"),
			"py": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>', "#06b6d4"),
			"go": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>', "#00add8"),
			"html": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="10" y1="17" x2="16" y2="17"/>', "#e44d26"),
			"css": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>', "#2563eb"),
			"pdf": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>', "#ef4444"),
			"docx": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="16" y2="17"/>', "#2563ef"),
			"xlsx": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="16" y2="17"/>', "#22c55e"),
			"csv": s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><path d="M10 12l-2 2 2 2"/>', "#22c55e"),
			"jpg": s('<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>', "#ec4899"),
			"png": s('<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>', "#ec4899"),
			"gif": s('<rect x="3" y="3" width="18" height="18" rx="2" ry="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>', "#ec4899"),
			"zip": s('<path d="M21 8l-3-3H7a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="12" y1="11" x2="12" y2="17"/><polyline points="9 14 12 11 15 14"/>', "#0284c7"),
		};
		return map[ext] || s('<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>', "#9ca3af");
	}



	// 格式化文件大小

	function formatFileSize(bytes) {

		if (bytes === 0) return '0 B';

		var k = 1024;

		var sizes = ['B', 'KB', 'MB', 'GB'];

		var i = Math.floor(Math.log(bytes) / Math.log(k));

		return (bytes / Math.pow(k, i)).toFixed(1) + ' ' + sizes[i];

	}

    // 日志功能

    function addLog(level, message) {

		var log = {

			time: new Date().toISOString(),

			level: level,

			message: message

		};

		logs.push(log);

		if (logs.length > 1000) logs.shift();

		// 修复：使用字符串拼接而不是模板字符串

		if (document.getElementById('logModal') && document.getElementById('logModal').classList.contains('active')) {

			renderLogs();

		}

	}

    function renderLogs() {

		var level = document.getElementById('logLevel').value;

		var search = document.getElementById('logSearch').value.toLowerCase();

		var filtered = logs.filter(function(log) {

			if (level !== 'all' && log.level !== level) return false;

			if (search && log.message.toLowerCase().indexOf(search) === -1) return false;

			return true;

		});

		var html = '';

		for (var i = filtered.length - 1; i >= 0; i--) {

			var log = filtered[i];

			var levelClass = 'log-level-' + log.level;

			html += '<div class="log-entry">' +

				'<span class="log-time">' + new Date(log.time).toLocaleTimeString() + '</span>' +

				'<span class="log-level ' + levelClass + '">' + log.level.toUpperCase() + '</span>' +

				'<span class="log-message">' + escapeHtml(log.message) + '</span>' +

				'</div>';

		}

		document.getElementById('logViewer').innerHTML = html || '暂无日志';

	}

	function updateTaskStatusInUI(taskId, status) {

		// 可以在任务面板中高亮显示状态变化

		console.log('任务状态更新:', taskId, status);

	}			

    function filterLogsLegacy() {

        renderLogs();

    }

    function clearLogs() {

        logs = [];

        renderLogs();

        addLog('info', '日志已清空');

    }

    function exportLogs() {

        var data = JSON.stringify(logs, null, 2);

        var blob = new Blob([data], {type: 'application/json'});

        var url = URL.createObjectURL(blob);

        var a = document.createElement('a');

        a.href = url;

        a.download = 'logs_' + new Date().toISOString() + '.json';

        a.click();

        URL.revokeObjectURL(url);

    }

    function openLogModalLegacy() {

        renderLogs();

        document.getElementById('logModal').classList.add('active');

    }

    function closeLogModal() {

        document.getElementById('logModal').classList.remove('active');

    }

    function openMonitorModal() {
        document.getElementById('monitorModal').classList.add('active');
        if (typeof loadMonitorData === 'function') loadMonitorData();
    }

    function closeMonitorModal() {
        document.getElementById('monitorModal').classList.remove('active');
        if (cacheChartInstance) { cacheChartInstance.destroy(); cacheChartInstance = null; } if (tokenChartInstance) { tokenChartInstance.destroy(); tokenChartInstance = null; }
    }

    function closeSidebarMore(focusButton) {
        var panel = document.getElementById('sidebarMorePanel');
        var button = document.getElementById('sidebarMoreButton');
        if (panel) panel.hidden = true;
        if (button) {
            button.setAttribute('aria-expanded', 'false');
            button.classList.remove('active');
            if (focusButton) button.focus();
        }
    }

    function positionSidebarPopup(panelID, buttonID) {
        var panel = document.getElementById(panelID);
        var button = document.getElementById(buttonID);
        if (!panel || panel.hidden || !button) return;
        var anchor = button.getBoundingClientRect();
        var narrow = window.matchMedia('(max-width: 768px)').matches;
        var left = narrow ? anchor.left : anchor.right + 12;
        var top = narrow ? anchor.bottom + 8 : anchor.top;
        panel.style.left = Math.max(8, Math.min(left, window.innerWidth - panel.offsetWidth - 8)) + 'px';
        panel.style.top = Math.max(8, Math.min(top, window.innerHeight - panel.offsetHeight - 8)) + 'px';
    }

    function positionSidebarMore() {
        positionSidebarPopup('sidebarMorePanel', 'sidebarMoreButton');
    }

    function positionSidebarTools() {
        positionSidebarPopup('sidebarToolsPanel', 'sidebarToolsButton');
    }

    function closeSidebarTools(focusButton) {
        var panel = document.getElementById('sidebarToolsPanel');
        var button = document.getElementById('sidebarToolsButton');
        if (panel) panel.hidden = true;
        if (button) {
            button.setAttribute('aria-expanded', 'false');
            button.classList.remove('active');
            if (focusButton) button.focus();
        }
    }

    function toggleSidebarTools(event) {
        if (event) {
            event.preventDefault();
            event.stopPropagation();
        }
        var panel = document.getElementById('sidebarToolsPanel');
        var button = document.getElementById('sidebarToolsButton');
        if (!panel || !button) return;
        if (!panel.hidden) {
            closeSidebarTools();
            return;
        }
        closeSidebarMore();
        closeCommonQuestions();
        panel.hidden = false;
        button.setAttribute('aria-expanded', 'true');
        button.classList.add('active');
        positionSidebarTools();
        panel.querySelector('[role="menuitem"]').focus();
    }

    function toggleSidebarMore(event) {
        if (event) {
            event.preventDefault();
            event.stopPropagation();
        }
        var panel = document.getElementById('sidebarMorePanel');
        var button = document.getElementById('sidebarMoreButton');
        if (!panel || !button) return;
        if (!panel.hidden) {
            closeSidebarMore();
            return;
        }
        closeSidebarTools();
        closeCommonQuestions();
        panel.hidden = false;
        button.setAttribute('aria-expanded', 'true');
        button.classList.add('active');
        positionSidebarMore();
        var first = panel.querySelector('button, a');
        if (first) first.focus();
    }

    window.toggleSidebarMore = toggleSidebarMore;
    window.closeSidebarMore = closeSidebarMore;
    window.toggleSidebarTools = toggleSidebarTools;
    window.closeSidebarTools = closeSidebarTools;
    window.addEventListener('resize', positionSidebarMore);
    window.addEventListener('resize', positionSidebarTools);
    document.addEventListener('scroll', positionSidebarTools, true);
    document.addEventListener('focusin', function(event) {
        var panel = document.getElementById('sidebarToolsPanel');
        var button = document.getElementById('sidebarToolsButton');
        if (panel && !panel.hidden && !panel.contains(event.target) && button && !button.contains(event.target)) closeSidebarTools();
    });
    document.addEventListener('click', function(event) {
        var panel = document.getElementById('sidebarMorePanel');
        var button = document.getElementById('sidebarMoreButton');
        if (panel && !panel.hidden && !panel.contains(event.target) && !button.contains(event.target)) closeSidebarMore();
        var tools = document.getElementById('sidebarToolsPanel');
        var trigger = document.getElementById('sidebarToolsButton');
        if (tools && !tools.hidden && !tools.contains(event.target) && trigger && !trigger.contains(event.target)) closeSidebarTools();
    });
    document.addEventListener('keydown', function(event) {
        var panel = document.getElementById('sidebarMorePanel');
        if (event.key === 'Escape' && panel && !panel.hidden) closeSidebarMore(true);
        var tools = document.getElementById('sidebarToolsPanel');
        var trigger = document.getElementById('sidebarToolsButton');
        if (!tools || !trigger) return;
        if (tools.hidden) {
            if (event.target === trigger && (event.key === 'ArrowDown' || event.key === 'ArrowUp')) {
                toggleSidebarTools(event);
                if (event.key === 'ArrowUp') tools.querySelectorAll('[role="menuitem"]')[1].focus();
            }
            return;
        }
        if (event.key === 'Escape') {
            event.preventDefault();
            closeSidebarTools(true);
        } else if (tools.contains(event.target) && ['ArrowDown', 'ArrowUp', 'Home', 'End'].indexOf(event.key) !== -1) {
            event.preventDefault();
            var items = Array.from(tools.querySelectorAll('[role="menuitem"]'));
            var index = items.indexOf(document.activeElement);
            if (event.key === 'Home') index = 0;
            else if (event.key === 'End') index = items.length - 1;
            else index = (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
            items[index].focus();
        }
    });

    // 侧边栏折叠

    function syncSidebarToggle(isCollapsed) {
        var button = document.getElementById('sidebarToggleBtn');
        if (!button) return;
        var key = isCollapsed ? 'sidebarExpand' : 'sidebarCollapse';
        var fallback = isCollapsed ? '展开侧边栏' : '收起侧边栏';
        var label = typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
        button.setAttribute('aria-expanded', isCollapsed ? 'false' : 'true');
        button.setAttribute('aria-label', label);
        button.setAttribute('data-i18n-title', key);
        button.title = label;
    }

    function toggleSidebar() {
        var sidebar = document.getElementById('sidebar');
        if (!sidebar) return;
        closeSidebarMore();
        closeSidebarTools();
        var isCollapsed = sidebar.classList.toggle('collapsed');
        syncSidebarToggle(isCollapsed);
        try { localStorage.setItem('macsSidebarCollapsed', isCollapsed ? 'true' : 'false'); } catch (e) {}
    }

    document.addEventListener('openkitty:languagechange', function() {
        var sidebar = document.getElementById('sidebar');
        if (sidebar) syncSidebarToggle(sidebar.classList.contains('collapsed'));
        positionSidebarTools();
    });

    window.toggleSidebar = toggleSidebar;

    function relocateWorkbenchNavigation(isNarrow) {
        var account = document.getElementById('topbarAuth');
        var accountSlot = document.getElementById('sidebarAccountSlot');
        var accountAnchor = document.getElementById('topbarAccountAnchor');
        if (account && accountSlot && accountAnchor) {
            closeAccountMenu(false);
            if (isNarrow) accountAnchor.parentElement.insertBefore(account, accountAnchor.nextSibling);
            else accountSlot.appendChild(account);
        }
        var slot = document.getElementById('sidebarWorkbenchSlot');
        var anchor = document.getElementById('topbarWorkbenchAnchor');
        var trigger = document.getElementById('workbenchMenuButton');
        var dropdown = trigger && trigger.closest ? trigger.closest('.topbar-workbench-dropdown') : null;
        if (!slot || !anchor || !dropdown) return;
        if (isNarrow) {
            if (dropdown.parentElement !== anchor.parentElement || dropdown.previousElementSibling !== anchor) {
                anchor.parentElement.insertBefore(dropdown, anchor.nextSibling);
            }
            return;
        }
        if (dropdown.parentElement !== slot) slot.appendChild(dropdown);
    }

    // Desktop starts expanded so product capabilities remain discoverable. A
    // user's explicit choice is preserved; narrow layouts always use the
    // horizontal mobile navigation defined in CSS.
    (function restoreSidebarState() {
        var applyState = function() {
            var sidebar = document.getElementById('sidebar');
            if (!sidebar) return;
            var isNarrow = window.matchMedia && window.matchMedia('(max-width: 768px)').matches;
            var saved = null;
            try { saved = localStorage.getItem('macsSidebarCollapsed'); } catch (e) {}
            var shouldCollapse = !isNarrow && saved === 'true';
            sidebar.classList.toggle('collapsed', shouldCollapse);
            syncSidebarToggle(shouldCollapse);
            relocateWorkbenchNavigation(isNarrow);
        };
        if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', applyState, { once: true });
        else applyState();
        if (window.matchMedia) {
            var sidebarMedia = window.matchMedia('(max-width: 768px)');
            var handleSidebarMedia = function(event) {
                var sidebar = document.getElementById('sidebar');
                if (!sidebar) return;
                if (event.matches) sidebar.classList.remove('collapsed');
                relocateWorkbenchNavigation(event.matches);
                syncSidebarToggle(sidebar.classList.contains('collapsed'));
            };
            if (sidebarMedia.addEventListener) sidebarMedia.addEventListener('change', handleSidebarMedia);
            else if (sidebarMedia.addListener) sidebarMedia.addListener(handleSidebarMedia);
        }
    })();

    // 模式切换

    function renderModeToggle() {

        var button = document.getElementById('modeToggleBtn');
        if (!button) return;

        var isCollab = currentMode === 'collab';
        var label = document.getElementById('modeToggleLabel');
        var singleIcon = document.getElementById('singleModeIcon');
        var collabIcon = document.getElementById('collabModeIcon');
        var t = typeof window.openKittyT === 'function'
            ? window.openKittyT
            : function(key, fallback) { return fallback || key; };

        button.classList.add('active');
        button.setAttribute('aria-pressed', isCollab ? 'true' : 'false');
        button.setAttribute('aria-label', isCollab
            ? t('multiAgentAria', '当前为多 Agent，点击切换到单 Agent')
            : t('singleAgentAria', '当前为单 Agent，点击切换到多 Agent'));
        button.title = isCollab
            ? t('switchToSingle', '切换到单 Agent')
            : t('switchToMulti', '切换到多 Agent');

        if (label) {
            // Keep this stable assignment for older extensions/static checks,
            // then let the active UI language replace it when necessary.
            label.textContent = isCollab ? '多Agent' : '单Agent';
            label.textContent = isCollab
                ? t('multiAgent', '多Agent')
                : t('singleAgent', '单Agent');
        }
        if (singleIcon) singleIcon.style.display = isCollab ? 'none' : '';
        if (collabIcon) collabIcon.style.display = isCollab ? '' : 'none';

    }

    function switchMode(mode) {

        currentMode = mode === 'collab' ? 'collab' : 'single';
        renderModeToggle();

        var agentSelector = document.getElementById('agentSelector');
        var messageInput = document.getElementById('messageInput');
        var t = typeof window.openKittyT === 'function'
            ? window.openKittyT
            : function(key, fallback) { return fallback || key; };

        if (currentMode === 'single') {

            if (agentSelector) agentSelector.style.display = window.composerInteractionMode === 'chat' ? 'none' : 'flex';
            if (messageInput) messageInput.placeholder = window.composerInteractionMode === 'chat' ? t('generationChatPrompt', '输入问题，模型直接回答（不执行工具）') : t('messageSingle', '输入消息... (Shift+Enter 换行，Enter 发送)');

        } else {

            if (agentSelector) agentSelector.style.display = 'none';
            if (messageInput) messageInput.placeholder = t('messageCollab', '输入讨论主题，点击发送后将打开协同配置...');

        }

        window.updateInteractionModeHint?.();

    }

    function toggleAgentMode() {

        switchMode(currentMode === 'single' ? 'collab' : 'single');

    }

    window.updateInteractionModeHint = function() {
        var select = document.getElementById('interactionModeSelect');
        var hint = document.getElementById('interactionModeHint');
        if (!select || !hint) return;
        var simple = (window.composerInteractionMode || select.value) === 'chat';
        var generation = simple && !!window.composerGenerationModel;
        var caption = generation
            ? settingsText('interactionGenerateNote', '直接生成')
            : simple ? settingsText('interactionChatNote', '直接回答') : settingsText('interactionAgentNote', '规划并执行');
        var explanation = generation
            ? settingsText('interactionGenerateHint', '直接调用所选图片、音乐或视频模型生成结果。')
            : simple ? settingsText('interactionChatHint', '直接与模型对话，不调用工具或执行工作流。')
                : settingsText('interactionAgentHint', 'AI会规划步骤、调用工具完成任务，可能进行多次模型调用。');
        hint.textContent = caption + ' · ' + explanation;
        hint.removeAttribute('title');
        hint.removeAttribute('aria-label');
        select.title = hint.textContent;
        select.setAttribute('aria-describedby', 'interactionModeHint');
    };

    window.setInteractionMode = function(mode) {
        if (window.composerInteractionMode && typeof getActiveTaskForSession === 'function' && getActiveTaskForSession(currentSessionId)) {
            document.getElementById('interactionModeSelect').value = window.composerInteractionMode;
            window.updateInteractionModeHint();
            showToast('当前任务仍在运行，请等待完成后再切换对话方式', 'info');
            return;
        }
        window.composerInteractionMode = mode === 'chat' ? 'chat' : 'agent';
        if (window.composerInteractionMode === 'agent') window.clearComposerGenerationModel?.();
        var simple = window.composerInteractionMode === 'chat';
        switchMode('single');
        document.getElementById('modeToggleBtn').disabled = simple;
        document.getElementById('interactionModeSelect').value = window.composerInteractionMode;
        if (simple && typeof window.clearSelectedSkillRun === 'function') window.clearSelectedSkillRun();
        var skillButton = document.getElementById('skillRunBtn');
        if (skillButton) skillButton.disabled = simple;
        if (simple) document.getElementById('messageInput').placeholder = settingsText('generationChatPrompt', '输入问题，模型直接回答（不执行工具）');
        if (typeof window.updateComposerModelAgent === 'function') window.updateComposerModelAgent();
        window.updateGenerationComposer?.();
        window.updateInteractionModeHint();
        try { localStorage.setItem('macsInteractionMode', window.composerInteractionMode); } catch (error) {}
    };

    document.addEventListener('openkitty:languagechange', function() {
        switchMode(currentMode);
        window.updateGenerationComposer?.();
        window.updateInteractionModeHint();
        var searchInput = document.getElementById('sessionSearchInput');
        if (searchInput && searchInput.value && typeof window.searchSessions === 'function') {
            window.searchSessions(searchInput.value);
        } else if (typeof loadSessions === 'function') {
            loadSessions();
        }
        if (currentConfig) {
            if (typeof renderAgentsList === 'function') renderAgentsList();
            if (typeof renderModelsList === 'function') renderModelsList();
            if (typeof renderChannelsList === 'function') renderChannelsList();
            if (typeof settingsRefreshConfigHealth === 'function') settingsRefreshConfigHealth();
            var modbusTargetCount = document.getElementById('modbusTargetCount');
            if (modbusTargetCount) {
                modbusTargetCount.textContent = String(Object.keys(currentConfig?.tools_config?.modbus?.targets || {}).length);
            }
        }
        var settingsEditor = document.getElementById('settingsItemEditorModal');
        if (settingsEditor) {
            var buttons = settingsEditor.querySelectorAll('.settings-editor-actions button');
            if (buttons[0]) buttons[0].textContent = settingsText('settingsCancel', '取消');
			if (buttons[1]) buttons[1].textContent = settingsText('settingsApplyEdit', '保存并应用');
		}
		settingsUpdateSaveUI();
    });

    document.addEventListener('DOMContentLoaded', function() {

		// Carry the current Agent into the single memory management surface.
		var memoryLink = document.querySelector('.memory-nav-link');
		if (memoryLink) {
			function updateMemoryLink() {
				var selected = document.getElementById('agentSelect');
				var memoryAgentName = (selected && selected.value) || window._defaultAgentName || '';
				var memoryQuery = new URLSearchParams();
				if (memoryAgentName) memoryQuery.set('agent', memoryAgentName);
				if (currentSessionId) memoryQuery.set('session', currentSessionId);
				memoryLink.href = '/web/memory.html' + (memoryQuery.size ? '?' + memoryQuery : '');
			}
			memoryLink.addEventListener('pointerdown', updateMemoryLink);
			memoryLink.addEventListener('focus', updateMemoryLink);
			memoryLink.addEventListener('click', updateMemoryLink);
		}

		console.log('DOM 加载完成，开始初始化...');
		switchMode(currentMode);

		// 恢复之前的 session_id

		try { localStorage.removeItem('currentSessionId'); } catch (e) {}
		fetch('/api/status').then(function(response) {
			if (!response.ok) throw new Error('status ' + response.status);
			return response.json();
		}).then(async function(status) {
			currentProjectId = String((status.project && status.project.project_id) || '');
			var requestedSession = new URLSearchParams(window.location.search).get('session');
			if (requestedSession && requestedSession.length <= 256) {
				var sessionResponse = await fetch('/api/sessions?limit=200', {cache: 'no-store'});
				if (!sessionResponse.ok) throw new Error('Unable to check requested session');
				var sessionList = await sessionResponse.json();
				if ((sessionList.sessions || []).some(function (session) { return session.id === requestedSession; })) {
					await loadSession(requestedSession);
					return;
				}
			}
			var savedSessionId = currentProjectId ? sessionStorage.getItem(activeSessionStorageKey(currentProjectId)) : '';
			if (!savedSessionId) return;
			currentSessionId = savedSessionId;
			loadSession(savedSessionId);
			setTimeout(function() { restorePendingTasks(savedSessionId); }, 800);
		}).catch(function(err) {
			console.warn('Unable to restore project-scoped session:', err);
		});

		// 初始化用户ID

		currentUserId = getUserId();

		addLog('info', '用户ID: ' + currentUserId);

		// 加载 Agents 和 Sessions

		loadAgents();

		loadSessions();
		if (new URLSearchParams(window.location.search).get('panel') === 'knowledge' && typeof window.openRagAdmin === 'function') window.openRagAdmin();
		if (new URLSearchParams(window.location.search).get('panel') === 'todo' && typeof window.openTodoPanel === 'function') window.openTodoPanel();

		// 后台预加载 Pipeline 和AI员工数据，等用户点击时瞬间打开
		if (typeof pipelineListCache !== 'undefined') {
			fetch('/api/pipeline/list').then(function(r){return r.json();}).then(function(d){
				pipelineListCache = d.pipelines || [];
			}).catch(function(){});
		}
		if (typeof aeWorkLogCache !== 'undefined') {
			fetch('/api/ae/worklog').then(function(r){return r.json();}).then(function(d){
				aeWorkLogCache = d;
			}).catch(function(){});
		}

		initPresetButtons();

		// 绑定 Enter 键事件

		var input = document.getElementById('messageInput');

		if (input) {

			// 移除可能存在的旧监听器

			input.removeEventListener('keydown', handleEnterKey);

			// 添加新的事件监听器

			input.addEventListener('keydown', handleEnterKey);

			console.log('✓ Enter 键监听已绑定');

			addLog('info', 'Enter 键发送功能已启用');

			// 可选：添加输入框焦点提示

			input.addEventListener('focus', function() {

				console.log('输入框已获得焦点，按 Enter 发送消息');

			});

		} else {

			console.error('✗ 找不到 messageInput 元素');

			addLog('error', '找不到输入框元素，请检查 HTML');

		}

		addLog('info', '系统初始化完成，当前会话: ' + (currentSessionId || '无'));

		console.log('初始化完成，isLoading:', isLoading);

		initSSE();

		startTaskAutoRefresh();
checkForUpdate();

	});



	window.addEventListener('beforeunload', function(event) {

		if (eventSource) {

			eventSource.close();

		}
		if (settingsDirty || settingsEditorDirty) {
			event.preventDefault();
			event.returnValue = '';
		}

	});









	function resetSendingState() {

		if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();

		var sendBtn = document.getElementById('sendBtn');

		if (sendBtn) {

			sendBtn.disabled = false;

		}

		var input = document.getElementById('messageInput');

		if (input && !isSessionEditable()) {

			input.disabled = false;

		}

		console.log('发送状态已重置');

	}

	// Restore loading bubbles for running tasks after page refresh




	

	function exportProgressLog(btn) {
		var root = btn ? btn.closest('[data-task-id], .message-bubble, .message') : null;
		var taskId = btn ? (btn.getAttribute('data-task-id') || '') : '';
		if (!taskId && root) taskId = root.getAttribute('data-task-id') || '';

		if (taskId) {
			downloadProgressLog(taskId);
			return;
		}

		var container = root || (btn ? btn.parentElement : document);
		var lines = [];
		var timeline = container ? container.querySelector('.progress-timeline') : null;
		if (timeline) {
			var bubbles = timeline.querySelectorAll('.progress-bubble');
			bubbles.forEach(function(b) { lines.push(b.textContent.trim()); });
		}
		var stream = container ? container.querySelector('.log-stream') : null;
		if (stream && stream.textContent.trim()) {
			lines.push(stream.textContent.trim());
		}
		if (lines.length === 0) {
			lines.push('(log not available)');
		}

		var blob = new Blob([lines.join('\n')], {type: 'text/plain;charset=utf-8'});
		var url = URL.createObjectURL(blob);
		var a = document.createElement('a');
		a.href = url;
		a.download = 'kitty_progress_' + new Date().toISOString().slice(0,19).replace(/:/g,'-') + '.log';
		a.style.display = 'none';
		document.body.appendChild(a);
		a.click();
		setTimeout(function() {
			document.body.removeChild(a);
			URL.revokeObjectURL(url);
		}, 1000);

	}



	function downloadProgressLog(taskId) {
		if (!taskId) return;
		var resolvedTaskId = resolveProgressLogTaskId(taskId);
		var a = document.createElement('a');
		a.href = progressLogDownloadUrl(taskId);
		a.download = 'progress_' + resolvedTaskId + '.log';
		a.style.display = 'none';
		document.body.appendChild(a);
		a.click();
		setTimeout(function(){ document.body.removeChild(a); }, 1000);
	}
	window.downloadProgressLog = downloadProgressLog;



	var sessionSearchTimer = null;
function compactSessionQuestion(value, fallback) {
    var text = String(value || '').replace(/\s+/g, ' ').trim();
    if (!text) return fallback || settingsText('noUserQuestion', '暂无用户问题');
    var chars = Array.from(text);
    return chars.length > 36 ? chars.slice(0, 36).join('') + '…' : text;
}
function sessionDisplayTitle(session) {
    var id = String(session.id || session.session_id || '');
    var name = String(session.name || '').trim();
    if (name && (!id || name.indexOf(id) < 0)) return compactSessionQuestion(name);
    var question = String(session.first_question || session.snippet || '').trim();
    return question ? compactSessionQuestion(question) : settingsText('newConversation', '新对话');
}
window.searchSessions = function(q) {
    var input = document.getElementById('sessionSearchInput');
    var clear = document.getElementById('searchClear');
    if (clear) clear.style.display = q ? 'block' : 'none';
    if (sessionSearchTimer) clearTimeout(sessionSearchTimer);
    sessionSearchTimer = setTimeout(function() {
        if (!q || q.trim().length === 0) { loadSessions(); return; }
        fetch('/api/sessions/search?q=' + encodeURIComponent(q))
            .then(function(r) { return r.json(); })
            .then(function(data) {
                var sessions = (data.sessions || []).filter(function (session) {
                    return session.message_count == null || Number(session.message_count) !== 0;
                });
                var list = document.getElementById('sessionsList');
                if (sessions.length === 0) {
                    list.innerHTML = '<div style=\"text-align:center;padding:20px;color:#9ca3af;font-size:calc(13.2px * var(--macs-font-scale, 1));\">' + escapeHtml(settingsText('noSearchResults', '未找到相关会话')) + '</div>';
                    return;
                }
                list.innerHTML = sessions.map(function(s) {
                    var question = compactSessionQuestion(s.first_question || s.snippet);
                    var title = sessionDisplayTitle(s);
                    return '<div class=\"session-item\" onclick=\"loadSession(' + jsCallArg(s.session_id) + ')\">' +
                        '<div class=\"session-name\">' + escapeHtml(title) + '</div>' +
                        (question === title ? '' : '<div class=\"session-question\">' + escapeHtml(question) + '</div>') +
                        '<div class=\"session-time\">' + (s.updated_at ? new Date(s.updated_at).toLocaleDateString() : '') + '</div>' +
                    '</div>';
                }).join('');
            })
            .catch(function(e) { console.error('Search error:', e); });
    }, 250);
};
window.clearSearch = function() {
    var input = document.getElementById('sessionSearchInput');
    if (input) { input.value = ''; }
    document.getElementById('searchClear').style.display = 'none';
    loadSessions();
};

async function loadSessions() {

        try {

            var response = await fetch('/api/sessions');

            var data = await response.json();

            var sessions = data.sessions || [];

            if (currentSessionId) {
                var hasCurrentSession = false;
                for (var j = 0; j < sessions.length; j++) {
                    if (sessions[j] && sessions[j].id === currentSessionId) {
                        hasCurrentSession = true;
                        break;
                    }
                }
                if (!hasCurrentSession) {
                    sessions.unshift({
                        id: currentSessionId,
                        name: settingsText('newConversation', '新对话'),
                        message_count: 0,
                        first_question: '',
                        updated_at: new Date().toISOString(),
                        local_placeholder: true
                    });
                }
            }

            sessions = sessions.filter(function (session) {
                return session.message_count == null || Number(session.message_count) !== 0;
            });

            var container = document.getElementById('sessionsList');

            if (sessions.length === 0) {

                container.innerHTML = '<div style="text-align: center; color: #adb5bd; padding: 20px;">' + escapeHtml(settingsText('noSessions', '暂无对话')) + '</div>';

                // A brand-new workspace with no saved sessions should still feel
                // immediately usable. Reuse the same prompt cloud as an empty
                // session instead of leaving the conversation canvas blank.
                if (!currentSessionId && typeof window.renderNewConversationState === 'function') {
                    var messagesArea = document.getElementById('messagesArea');
                    if (messagesArea && !messagesArea.querySelector('.message, .session-recovery-panel')) {
                        messagesArea.innerHTML = window.renderNewConversationState();
                        if (typeof setSessionHeaderTitle === 'function') {
                            setSessionHeaderTitle(settingsText('newConversation', '新对话'));
                        }
                        var welcomeTips = document.getElementById('welcomeTips');
                        if (welcomeTips) welcomeTips.style.display = '';
                    }
                }

                return;

            }

            var html = '';

            for (var i = 0; i < sessions.length; i++) {

                var s = sessions[i];

                var activeClass = (currentSessionId === s.id) ? 'active' : '';

                var sessionName = sessionDisplayTitle(s);

                var messageCount = s.message_count || 0;

                var updateTime = formatDate(s.updated_at);

                var firstQuestion = compactSessionQuestion(
                    s.first_question,
                    s.local_placeholder ? settingsText('firstQuestionPending', '等待你的第一个问题') : settingsText('noUserQuestion', '暂无用户问题')
                );

                // 判断会话来源

                var sourceIcon = '🌐';

                if (s.id.startsWith('qq_')) sourceIcon = '🐧';

                else if (s.id.startsWith('feishu_')) sourceIcon = '🐦';

                else if (s.id.startsWith('dingtalk_')) sourceIcon = '📱';

                else if (s.id.startsWith('wework_')) sourceIcon = '💼';

                html += '<div class="session-item ' + activeClass + '" onclick="loadSession(' + jsCallArg(s.id) + ')">' +

                    '<div class="session-name">' + sourceIcon + ' ' + escapeHtml(sessionName) + '</div>' +

                    (firstQuestion === sessionName ? '' : '<div class="session-question">' + escapeHtml(firstQuestion) + '</div>') +

                    '<div class="session-time">' + (messageCount > 0 ? messageCount + ' ' + escapeHtml(settingsText('messageCountUnit', '条消息')) + ' · ' : '') + updateTime + '</div>' +

                    '</div>';

            }

            container.innerHTML = html;

        } catch (err) {

            console.error('加载会话失败:', err);

            addLog('error', '加载会话失败: ' + err.message);

        }

    }


    function showToast(msg, type) {
        type = type || 'info';
        var toast = document.getElementById('toast');
        if (!toast) {
            toast = document.createElement('div');
            toast.id = 'toast';
            toast.className = 'toast';
            document.body.appendChild(toast);
        }
        toast.textContent = msg;
        toast.className = 'toast toast-' + type;
        toast.classList.add('show');
        clearTimeout(toast._timeout);
        toast._timeout = setTimeout(function() {
            toast.classList.remove('show');
        }, type === 'error' ? 4000 : 2500);
    }



	// Preview a collaboration output file (meeting minutes, delivery) in the modal




	// Unified message attachment state. Documents and images use the same
	// upload, send, persistence, and history-rendering contract.
	let pendingMessageAttachments = [];
	let nextMessageAttachmentID = 1;

	function attachmentFileKind(file) {
		return file && String(file.type || '').toLowerCase().indexOf('image/') === 0 ? 'image' : 'file';
	}

	function queueMessageAttachment(file, previewURL, revokePreviewURL) {
		if (!file) return null;
		if (window.composerGenerationModel && !window.acceptGenerationAttachment(file, pendingMessageAttachments)) {
			if (revokePreviewURL && previewURL) URL.revokeObjectURL(previewURL);
			return null;
		}
		if (pendingMessageAttachments.length >= 20) {
			showToast('每条消息最多添加 20 个附件', 'error');
			return null;
		}
		var item = {
			id: 'attachment_' + (nextMessageAttachmentID++),
			name: file.name || ('attachment_' + Date.now()),
			size: file.size || 0,
			mime_type: file.type || 'application/octet-stream',
			kind: attachmentFileKind(file),
			preview_url: previewURL || '',
			revoke_preview_url: !!revokePreviewURL,
			path: '',
			status: 'uploading',
			error: ''
		};
		pendingMessageAttachments.push(item);
		renderPendingMessageAttachments();

		var formData = new FormData();
		formData.append('file', file, item.name);
		item.uploadPromise = fetch('/api/upload', { method: 'POST', body: formData })
			.then(function(resp) {
				return resp.json().catch(function() { return {}; }).then(function(data) {
					if (!resp.ok || !data.success || !data.path) {
						throw new Error(data.error || ('HTTP ' + resp.status));
					}
					item.name = data.filename || item.name;
					item.path = data.path;
					item.size = Number(data.size || item.size || 0);
					item.mime_type = data.type || item.mime_type;
					item.kind = data.kind || attachmentFileKind(file);
					item.status = 'ready';
					addLog('info', '附件已上传: ' + item.name);
				});
			})
			.catch(function(err) {
				item.status = 'error';
				item.error = err && err.message ? err.message : '上传失败';
				addLog('error', '上传失败: ' + item.error);
			})
			.then(function() {
				renderPendingMessageAttachments();
				return item;
			});
		return item;
	}

	async function handleFileUpload(input) {
		var files = Array.prototype.slice.call(input && input.files ? input.files : []);
		for (var i = 0; i < files.length; i++) {
			var file = files[i];
			var previewURL = '';
			var revokePreviewURL = false;
			if (attachmentFileKind(file) === 'image' && window.URL && URL.createObjectURL) {
				previewURL = URL.createObjectURL(file);
				revokePreviewURL = true;
			}
			queueMessageAttachment(file, previewURL, revokePreviewURL);
		}
		if (input) input.value = '';
	}



	var mediaRecorder = null;
	var audioChunks = [];

window.toggleVoiceInput = function() {
	var btn = document.getElementById('micBtn');
	if (!btn) return;

	if (mediaRecorder && mediaRecorder.state === 'recording') {
		// Stop recording
		mediaRecorder.stop();
		btn.style.color = '#64748b';
		btn.querySelector('i').setAttribute('data-lucide', 'mic');
		lucide.createIcons();
		return;
	}

	// Start recording via MediaRecorder → server Whisper
	navigator.mediaDevices.getUserMedia({ audio: true }).then(function(stream) {
		audioChunks = [];
		mediaRecorder = new MediaRecorder(stream, { mimeType: 'audio/webm' });
		mediaRecorder.ondataavailable = function(e) { if (e.data.size > 0) audioChunks.push(e.data); };
		mediaRecorder.onstop = function() {
			stream.getTracks().forEach(function(t) { t.stop(); });
			var blob = new Blob(audioChunks, { type: 'audio/webm' });
			var fd = new FormData();
			fd.append('file', blob, 'recording.webm');
			fd.append('lang', 'zh');

			var input = document.getElementById('messageInput');
			if (input) { input.placeholder = '⏳ 转写中...'; }

			fetch('/api/voice', { method: 'POST', body: fd }).then(function(r) { return r.json(); }).then(function(d) {
				if (input) input.placeholder = '输入消息... (Ctrl+V 粘贴图片 | Shift+Enter 换行 | Enter 发送)';
				if (d.ok && input) {
					input.value = (input.value ? input.value + ' ' : '') + d.text;
				} else if (!d.ok) {
					showToast(d.error || '转录失败', 'error');
				}
			}).catch(function(e) {
				if (input) input.placeholder = '输入消息...';
				showToast('转录失败: ' + e.message, 'error');
			});
		};

		mediaRecorder.start();
		btn.style.color = '#ef4444';
		btn.querySelector('i').setAttribute('data-lucide', 'mic');
		lucide.createIcons();
		var input2 = document.getElementById('messageInput');
		if (input2) { input2.focus(); input2.placeholder = '🎤 正在录音...'; }
	}).catch(function(e) {
		showToast('麦克风权限被拒绝或设备不可用', 'error');
	});
};

function attachmentStatusText(item) {
		if (item.status === 'uploading') return '上传中…';
		if (item.status === 'error') return '上传失败';
		return formatFileSize(item.size || 0);
	}

	function renderPendingMessageAttachments() {
		var fileArea = document.getElementById('uploadArea');
		var fileNameArea = document.getElementById('uploadFileName');
		var imageArea = document.getElementById('imagePreviewArea');
		var files = pendingMessageAttachments.filter(function(item) { return item.kind !== 'image'; });
		var images = pendingMessageAttachments.filter(function(item) { return item.kind === 'image'; });

		if (fileArea && fileNameArea) {
			if (files.length === 0) {
				fileArea.style.display = 'none';
				fileNameArea.innerHTML = '';
			} else {
				fileArea.style.display = 'block';
				fileNameArea.innerHTML = files.map(function(item) {
					var errorTitle = item.error ? ' title="' + escapeHtml(item.error) + '"' : '';
					return '<span class="composer-attachment ' + escapeHtml(item.status) + '"' + errorTitle + '>' +
						'<span class="composer-attachment-name">📎 ' + escapeHtml(item.name) + '</span>' +
						'<span class="composer-attachment-meta">' + escapeHtml(attachmentStatusText(item)) + '</span>' +
						'<button type="button" onclick="removePendingMessageAttachment(\'' + escapeHtml(item.id) + '\')" aria-label="移除附件">✕</button>' +
						'</span>';
				}).join('');
			}
		}

		if (imageArea) {
			if (images.length === 0) {
				imageArea.style.display = 'none';
				imageArea.innerHTML = '';
			} else {
				imageArea.style.display = 'flex';
				imageArea.innerHTML = images.map(function(item) {
					var preview = item.preview_url || (item.path ? '/api/files/download?inline=1&path=' + encodeURIComponent(item.path) : '');
					return '<div class="composer-image ' + escapeHtml(item.status) + '" title="' + escapeHtml(item.error || item.name) + '">' +
						(preview ? '<img src="' + escapeHtml(preview) + '" alt="' + escapeHtml(item.name) + '">' : '<span>🖼️</span>') +
						'<span class="composer-image-status">' + escapeHtml(attachmentStatusText(item)) + '</span>' +
						'<button type="button" onclick="removePendingMessageAttachment(\'' + escapeHtml(item.id) + '\')" aria-label="移除图片">✕</button>' +
						'</div>';
				}).join('');
			}
		}
	}

	function removePendingMessageAttachment(id) {
		var kept = [];
		for (var i = 0; i < pendingMessageAttachments.length; i++) {
			var item = pendingMessageAttachments[i];
			if (item.id === id) {
				if (item.revoke_preview_url && item.preview_url && window.URL && URL.revokeObjectURL) {
					URL.revokeObjectURL(item.preview_url);
				}
			} else {
				kept.push(item);
			}
		}
		pendingMessageAttachments = kept;
		renderPendingMessageAttachments();
	}

	function clearUpload() {
		pendingMessageAttachments.slice().forEach(function(item) {
			if (item.kind !== 'image') removePendingMessageAttachment(item.id);
		});
	}

	function clearPendingMessageAttachments() {
		pendingMessageAttachments.slice().forEach(function(item) {
			removePendingMessageAttachment(item.id);
		});
	}

	async function getPendingMessageAttachments() {
		var snapshot = pendingMessageAttachments.slice();
		await Promise.all(snapshot.map(function(item) { return item.uploadPromise || Promise.resolve(item); }));
		var failed = snapshot.find(function(item) { return item.status !== 'ready' || !item.path; });
		if (failed) {
			throw new Error('附件“' + failed.name + '”上传失败，请移除后重试');
		}
		return snapshot.map(function(item) {
			return {
				name: item.name,
				path: item.path,
				mime_type: item.mime_type,
				size: item.size,
				kind: item.kind
			};
		});
	}

	window.removePendingMessageAttachment = removePendingMessageAttachment;
	window.clearPendingMessageAttachments = clearPendingMessageAttachments;
	window.getPendingMessageAttachments = getPendingMessageAttachments;
	window.hasPendingMessageAttachments = function() { return pendingMessageAttachments.length > 0; };
	window.addReadyMessageAttachment = function(file) {
		pendingMessageAttachments.push(Object.assign({}, file, {id:'attachment_' + (nextMessageAttachmentID++), status:'ready', preview_url: '/api/files/download?inline=1&path=' + encodeURIComponent(file.path)}));
		renderPendingMessageAttachments();
	};

	// ====== Multimodal: image paste, drop, upload ======
	document.addEventListener('paste', function(e) {
		var input = document.getElementById('messageInput');
		if (!input || document.activeElement !== input) return;
		var items = e.clipboardData && e.clipboardData.items;
		if (!items) return;
		for (var i = 0; i < items.length; i++) {
			if (items[i].type.indexOf('image') === 0) {
				e.preventDefault();
				var imageFile = items[i].getAsFile();
				if (imageFile) {
					var imageName = imageFile.name || ('paste_' + Date.now() + '.png');
					var namedImage = imageFile.name ? imageFile : new File([imageFile], imageName, {type: imageFile.type || 'image/png'});
					var pastePreview = window.URL && URL.createObjectURL ? URL.createObjectURL(namedImage) : '';
					queueMessageAttachment(namedImage, pastePreview, !!pastePreview);
				}
			}
		}
	});

	var dropTarget = document.querySelector('.input-container') || document.getElementById('messageInput');
	if (dropTarget) {
		dropTarget.addEventListener('dragover', function(e) { e.preventDefault(); });
		dropTarget.addEventListener('drop', function(e) {
			e.preventDefault();
			var files = e.dataTransfer && e.dataTransfer.files ? e.dataTransfer.files : [];
			for (var i = 0; i < files.length; i++) {
				var dropped = files[i];
				var preview = attachmentFileKind(dropped) === 'image' && window.URL && URL.createObjectURL ? URL.createObjectURL(dropped) : '';
				queueMessageAttachment(dropped, preview, !!preview);
			}
		});
	}

	var imageButton = document.getElementById('imageBtn');
	if (imageButton) {
		imageButton.addEventListener('click', function() {
			document.getElementById('imageInput').click();
		});
	}

	function handleImageSelect(input) {
		var files = Array.prototype.slice.call(input && input.files ? input.files : []);
		for (var i = 0; i < files.length; i++) {
			var file = files[i];
			var preview = window.URL && URL.createObjectURL ? URL.createObjectURL(file) : '';
			queueMessageAttachment(file, preview, !!preview);
		}
		if (input) input.value = '';
	}
	window.handleImageSelect = handleImageSelect;


	// Click the hidden file input when upload button is clicked

	document.addEventListener('DOMContentLoaded', function() {
		try { window._lastCompletedTaskId = window._lastCompletedTaskId || localStorage.getItem('lastCompletedTaskId'); window._lastDuration = window._lastDuration || localStorage.getItem('lastDuration'); } catch(e) {}


		var uploadBtn = document.getElementById('uploadBtn');

		var fileInput = document.getElementById('fileInput');

		if (uploadBtn && fileInput) {

			uploadBtn.addEventListener('click', function() { fileInput.click(); });

		}

	});










	document.querySelectorAll('input[name="collabMode"]').forEach(function(radio) {

		radio.addEventListener('change', function() {

			var hostSelector = document.getElementById('hostSelector');

			if (this.value === 'group_chat_with_host') {

				hostSelector.style.display = 'block';

			} else {

				hostSelector.style.display = 'none';

			}

		});

	});
















    async function openConfigModal(defaultTab = 'models') {

		try {

			await loadConfigToForm();

			// 添加空值检查 - 防止 Cannot set properties of null 错误

			var gatewayPortEl = document.getElementById('gatewayPort');

			if (gatewayPortEl) gatewayPortEl.value = currentConfig?.gateway?.listen_addr?.replace(':', '') || '8080';

			var apiKeyEl = document.getElementById('apiKey');

			if (apiKeyEl) apiKeyEl.value = currentConfig?.gateway?.api_key || '';

			var modelConfigEl = document.getElementById('modelConfig');

			if (modelConfigEl) modelConfigEl.value = JSON.stringify(currentConfig?.model_config || {}, null, 2);

			var modal = document.getElementById('configModal');

			if (modal) modal.classList.add('active');
			settingsSetDirty(false);

			switchConfigTab(defaultTab);

		} catch (err) {

			console.error('打开配置失败:', err);

			addLog('error', '打开配置失败: ' + err.message);

		}

	}

	function closeConfigModal(force) {
		if (settingsSaving && force !== true) return false;
		if (force !== true && !settingsConfirmDiscard('settingsDiscardChangesConfirm', '有未保存的配置更改，确定放弃吗？')) {
			return false;
		}
		closeSettingsItemEditor(true);
		var modal = document.getElementById('configModal');
		if (modal) modal.classList.remove('active');
		settingsSetDirty(false);
		return true;
	}

	async function reloadConfig() {
		if (!settingsConfirmDiscard('settingsReloadDiscardConfirm', '重新加载会放弃未保存的配置更改，确定继续吗？')) return false;
		try {
			var response = await fetch('/api/config/reload', { method: 'POST' });
			var result = await response.json().catch(function() { return {}; });
			if (!response.ok || result.status !== 'success') {
				throw new Error(result.message || ('HTTP ' + response.status));
			}
			closeSettingsItemEditor(true);
			await loadConfigToForm();
			settingsSetDirty(false);
			alert(result.message || '配置已重新加载');
			addLog('info', result.message || '配置已重新加载');
			return true;
		} catch (err) {
			alert('重新加载失败: ' + err.message);
			addLog('error', '重新加载失败: ' + err.message);
			return false;
		}
	}

    function escapeHtml(str) {

		if (str == null) return '';
		// Ensure it's a string, not a DOM element or object
		if (typeof str !== 'string') {
			if (str.textContent !== undefined) str = str.textContent;
			else if (typeof str.toString === 'function') str = str.toString();
			else str = String(str);
		}
		if (!str) return '';

		var div = document.createElement('div');
		div.textContent = str;
		return div.innerHTML;

	}

    function escapeAttr(str) {
        return escapeHtml(str).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }

    function jsCallArg(value) {
        if (value == null) value = '';
        return escapeAttr(JSON.stringify(String(value)));
    }

    function formatDate(dateStr) {

        if (!dateStr) return '';

        var date = new Date(dateStr);

        var y = date.getFullYear();

        var mo = String(date.getMonth() + 1).padStart(2, '0');

        var d = String(date.getDate()).padStart(2, '0');

        var h = String(date.getHours()).padStart(2, '0');

        var mi = String(date.getMinutes()).padStart(2, '0');

        var s = String(date.getSeconds()).padStart(2, '0');

        return y + '-' + mo + '-' + d + ' ' + h + ':' + mi + ':' + s;

    }

    function formatDuration(ms) {
        if (!ms && ms !== 0) return '-';
        return (ms / 1000).toFixed(1) + 's';
    }

    function scrollToBottom() {
        var container = document.getElementById('messagesArea');
        if (!container) return;
        // Use requestAnimationFrame to ensure scroll happens after DOM reflow
        requestAnimationFrame(function() {
            container.scrollTop = container.scrollHeight;
            // Double-check after images/rendered content may have loaded
            setTimeout(function() {
                container.scrollTop = container.scrollHeight;
            }, 100);
        });
    }



    let eventSource = null;

	let reconnectAttempts = 0;

	const maxReconnectAttempts = 5;





	// Loading watchdog: show refresh hint if task waits too long

		var _loadingWatchdog = setInterval(function() {
			if (!isLoading) return;
			var idle = Date.now() - (window._lastProgress || Date.now());
			var idleSec = Math.floor(idle / 1000);
			// Update loading text to show idle seconds after 15s
			var texts = document.querySelectorAll(".loading-text");
			texts.forEach(function(t) {
				if (idle >= 15000) {
					t.textContent = "处理中，已等待 " + idleSec + " 秒...";
				}
			});
			// Show refresh hint after 60s of no progress
			if (idle > 60000) {
				var hints = document.querySelectorAll(".loading-refresh-hint");
				hints.forEach(function(h) { h.style.display = "block"; });
			}
		}, 5000);










	// 改进日志查看函数

	async function openLogModal() {

		try {

			const response = await fetch('/api/logs?lines=200');

			const data = await response.json();

			// 渲染日志 - 使用字符串拼接而不是模板字符串

			let html = '';

			for (let i = 0; i < data.logs.length; i++) {

				const log = data.logs[i];

				const level = log.level || 'info';

				const levelClass = 'log-level-' + level;

				const levelText = level.toUpperCase();

				const timestamp = log.timestamp || '';

				const message = escapeHtml(log.message || log.raw || '');

				html += '<div class="log-entry">' +

					'<span class="log-time">' + timestamp + '</span>' +

					'<span class="log-level ' + levelClass + '">' + levelText + '</span>' +

					'<span class="log-message">' + message + '</span>' +

					'</div>';

			}

			document.getElementById('logViewer').innerHTML = html || '暂无日志';

			document.getElementById('logModal').classList.add('active');

		} catch (err) {

			console.error('加载日志失败:', err);

			document.getElementById('logViewer').innerHTML = '加载日志失败: ' + err.message;

			document.getElementById('logModal').classList.add('active');

		}

	}



	let isLogFullscreen = false;

    let originalModalContent = null;



	function toggleLogFullscreen() {

		const modal = document.getElementById('logModal');

		if (!isLogFullscreen) {

			// 进入全屏

			originalModalContent = modal.innerHTML;

			const logControls = document.querySelector('.log-controls').cloneNode(true);

			const logViewer = document.getElementById('logViewer');

			// 修复：flex-direction: column（不是 colorn）

			modal.innerHTML = '<div class="fullscreen-log-container" style="position: fixed; top: 0; left: 0; width: 100vw; height: 100vh; background: #1e1e1e; display: flex; flex-direction: column; z-index: 10000;">' +

				'<div style="padding: 12px 20px; background: #2d2d2d; display: flex; justify-content: space-between; align-items: center; flex-shrink: 0;">' +

				'<h3 style="margin: 0; color: white; font-size: calc(19.8px * var(--macs-font-scale, 1));">📋 执行日志 - 全屏模式</h3>' +

				'<button onclick="toggleLogFullscreen()" style="padding: 6px 16px; background: #dc3545; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: calc(15.4px * var(--macs-font-scale, 1));">✕ 退出全屏</button>' +

				'</div>' +

				'<div class="log-controls" style="padding: 12px 20px; background: #252525; flex-shrink: 0;"></div>' +

				'<div id="fullscreenLogViewer" style="flex: 1; overflow-y: auto; padding: 16px; min-height: 0;"></div>' +

				'</div>';

			// 复制控件

			const newControls = modal.querySelector('.log-controls');

			newControls.innerHTML = logControls.innerHTML;

			// 复制日志内容

			const newLogViewer = modal.querySelector('#fullscreenLogViewer');

			newLogViewer.innerHTML = logViewer.innerHTML;

			newLogViewer.classList.add('log-viewer');

			// 重新绑定控件事件

			const newLogLevel = newControls.querySelector('#logLevel');

			const newLogSearch = newControls.querySelector('#logSearch');

			const newClearBtn = newControls.querySelector('button[onclick="clearLogs()"]');

			const newExportBtn = newControls.querySelector('button[onclick="exportLogs()"]');

			if (newLogLevel) newLogLevel.onchange = function() { filterLogsFullscreen(); };

			if (newLogSearch) newLogSearch.onkeyup = function() { filterLogsFullscreen(); };

			if (newClearBtn) newClearBtn.onclick = function() { clearLogsFullscreen(); };

			if (newExportBtn) newExportBtn.onclick = function() { exportLogs(); };

			// 恢复日志查看器的样式

			if (newLogViewer) {

				newLogViewer.style.cssText = 'flex: 1; overflow-y: auto; padding: 16px; background: #1e1e1e; font-family: Monaco, Menlo, monospace; font-size: calc(13.2px * var(--macs-font-scale, 1));';

			}

			isLogFullscreen = true;

		} else {

			// 退出全屏

			modal.innerHTML = originalModalContent;

			originalModalContent = null;

			isLogFullscreen = false;

			// 重新加载日志

			openLogModal();

		}

	}



	// 全屏模式下的过滤函数

	function filterLogsFullscreen() {

		var levelEl = document.querySelector('#fullscreenLogViewer') ? 

			document.querySelector('#logModal .log-controls #logLevel') : null;

		if (!levelEl) {

			// 如果没有找到，尝试另一种方式

			levelEl = document.querySelector('#logModal #logLevel');

		}

		var level = levelEl ? levelEl.value : 'all';

		var searchEl = document.querySelector('#logModal .log-controls #logSearch');

		var search = searchEl ? searchEl.value : '';

		var url = '/api/logs?lines=500&level=' + encodeURIComponent(level) + '&search=' + encodeURIComponent(search);

		fetch(url)

			.then(function(response) { return response.json(); })

			.then(function(data) {

				var html = '';

				for (var i = 0; i < data.logs.length; i++) {

					var log = data.logs[i];

					var logLevel = log.level || 'info';

					var levelClass = 'log-level-' + logLevel;

					var levelText = logLevel.toUpperCase();

					var timestamp = log.timestamp || '';

					var message = escapeHtml(log.message || log.raw || '');

					html += '<div class="log-entry" style="padding: 6px 0; border-bottom: 1px solid #333; font-family: monospace;">' +

						'<span class="log-time" style="color: #6a9955; margin-right: 12px;">' + timestamp + '</span>' +

						'<span class="log-level ' + levelClass + '" style="display: inline-block; padding: 2px 6px; border-radius: 4px; font-size: calc(11px * var(--macs-font-scale, 1)); margin-right: 12px;">' + levelText + '</span>' +

						'<span class="log-message" style="color: #ce9178;">' + message + '</span>' +

						'</div>';

				}

				var logViewer = document.getElementById('fullscreenLogViewer');

				if (logViewer) {

					logViewer.innerHTML = html || '<div style="text-align: center; padding: 40px; color: #6c757d;">暂无日志</div>';

				}

			})

			.catch(function(err) {

				console.error('过滤日志失败:', err);

				var logViewer = document.getElementById('fullscreenLogViewer');

				if (logViewer) {

					logViewer.innerHTML = '<div style="color: #dc3545; padding: 20px;">加载失败: ' + err.message + '</div>';

				}

			});

	}



	function clearLogsFullscreen() {

		if (confirm('确定要清空所有日志吗？')) {

			logs = [];

			var logViewer = document.getElementById('fullscreenLogViewer');

			if (logViewer) {

				logViewer.innerHTML = '<div style="text-align: center; padding: 40px; color: #6c757d;">日志已清空</div>';

			}

			addLog('info', '日志已清空');

		}

	}				



	// 添加过滤日志功能

	async function filterLogs() {

		const level = document.getElementById('logLevel').value;

		const search = document.getElementById('logSearch').value;

		try {

			const url = '/api/logs?lines=500&level=' + encodeURIComponent(level) + '&search=' + encodeURIComponent(search);

			const response = await fetch(url);

			const data = await response.json();

			let html = '';

			for (let i = 0; i < data.logs.length; i++) {

				const log = data.logs[i];

				const logLevel = log.level || 'info';

				const levelClass = 'log-level-' + logLevel;

				const levelText = logLevel.toUpperCase();

				const timestamp = log.timestamp || '';

				const message = escapeHtml(log.message || log.raw || '');

				html += '<div class="log-entry">' +

					'<span class="log-time">' + timestamp + '</span>' +

					'<span class="log-level ' + levelClass + '">' + levelText + '</span>' +

					'<span class="log-message">' + message + '</span>' +

					'</div>';

			}

			document.getElementById('logViewer').innerHTML = html || '暂无日志';

		} catch (err) {

			console.error('过滤日志失败:', err);

		}

	}

	document.getElementById('helpModal').addEventListener('click', function(e) {

		if (e.target === this) {

			closeHelpModal();

		}

	});	



	function switchConfigTab(tab) {
        if (tab === 'email') {
            tab = 'channels';
            document.getElementById('settingsEmailCard').open = true;
        }
        document.querySelectorAll('.config-tab').forEach(function(button) {
            var active = button.getAttribute('onclick') === "switchConfigTab('" + tab + "')";
            button.classList.toggle('active', active);
            button.setAttribute('aria-current', active ? 'page' : 'false');
        });
        document.querySelectorAll('#configForm > .config-section').forEach(function(section) {
            section.style.display = section.id === tab + 'Config' ? 'block' : 'none';
        });
        var copy = {
            models: ['modelsConfigTab', 'AI 服务', 'settingsModelsIntro', '连接一个 AI 服务，助手就能开始帮你处理工作。'],
            agents: ['agentsConfigTab', '我的助手', 'settingsAgentsIntro', '选择平时使用的助手，告诉它你希望怎样回答和做事。'],
            basic: ['basicConfigTab', '工作文件', 'settingsFilesIntro', '设置助手工作的文件夹，以及生成文件的保存位置。'],
            tools: ['toolsConfigTab', '助手能做什么', 'settingsToolsIntro', '选择助手可以使用哪些工具，帮你读取网页、处理文档或操作应用。'],
            channels: ['channelsConfigTab', '连接应用', 'settingsAppsIntro', '连接常用应用，让助手在你熟悉的地方接收任务。'],
            gateway: ['gatewayConfigTab', '高级设置', 'settingsAdvancedIntro', '适合需要调整部署、性能或设备连接的人；日常使用可保留当前值。']
        }[tab];
        var intro = document.getElementById('settingsPageIntro');
        if (intro && copy) intro.innerHTML = '<h2>' + escapeHtml(settingsText(copy[0], copy[1])) + '</h2><p>' + escapeHtml(settingsText(copy[2], copy[3])) + '</p>';
        var health = document.getElementById('configHealthBox');
        if (health) health.hidden = tab !== 'models' && tab !== 'agents';
        if (tab === 'agents') renderAgentsList();
        if (tab === 'models') renderModelsList();
        if (tab === 'tools') renderSettingsCapabilities();
        if (tab === 'channels') { renderChannelsList(); renderSettingsAppChoices(); }
        var page = document.querySelector('.settings-page');
        if (page) page.scrollTop = 0;
    }


	async function fetchConfigSectionsWithRetry() {
		var lastErr = null;
		for (var attempt = 0; attempt < 3; attempt++) {
			try {
				var response = await fetch('/api/config/sections', {cache: 'no-store'});
				if (!response.ok) {
					throw new Error('HTTP ' + response.status);
				}
				return response;
			} catch (err) {
				lastErr = err;
				if (attempt < 2) {
					await new Promise(function(resolve) { setTimeout(resolve, 300 * (attempt + 1)); });
				}
			}
		}
		throw lastErr || new Error('Failed to fetch');
	}

	async function loadConfigToForm() {

		try {

			const response = await fetchConfigSectionsWithRetry();

			currentConfig = await response.json();
			// Recompute display messages in the currently selected UI language.
			// The API status is useful as structured input, but its human-readable
			// strings may have been produced in a different language.
			settingsRefreshConfigHealth();



			// Keep credentials and application configuration out of the console.

			// 基础配置

			const teamNameEl = document.getElementById('teamName');

			if (teamNameEl) teamNameEl.value = currentConfig.team_name || '';

			const workspaceEl = document.getElementById('workspace');        if (workspaceEl) workspaceEl.value = currentConfig.workspace || '';

			const memoryEnabledEl = document.getElementById('memoryEnabled');
			if (memoryEnabledEl) memoryEnabledEl.checked = currentConfig.memory_config?.enabled !== false;

			const outputDirEl = document.getElementById('outputDir');

			if (outputDirEl) outputDirEl.value = (currentConfig.output && currentConfig.output.dir) || 'storage';

			const outputFormatEl = document.getElementById('outputFormat');

			if (outputFormatEl) outputFormatEl.value = (currentConfig.output && currentConfig.output.format) || 'json';

			const semaphoreLimitEl = document.getElementById('semaphoreLimit');

			if (semaphoreLimitEl) semaphoreLimitEl.value = (currentConfig.runtime && currentConfig.runtime.semaphore_limit) || 4;

			const headlessEl = document.getElementById('headless');

			if (headlessEl) headlessEl.checked = currentConfig.headless || false;

			const privateNetworkProtectionEl = document.getElementById('privateNetworkProtection');

			if (privateNetworkProtectionEl) privateNetworkProtectionEl.checked = !!(currentConfig.tools_config && currentConfig.tools_config.private_network_protection);

			const debugToolEnabledEl = document.getElementById('debugToolEnabled');

			if (debugToolEnabledEl) {
				const disabledTools = (currentConfig.tools_config && Array.isArray(currentConfig.tools_config.disabled_tools)) ? currentConfig.tools_config.disabled_tools : [];
				debugToolEnabledEl.checked = !disabledTools.some(function(name) { return String(name || '').trim().toLowerCase() === 'debug'; });
			}

			const debugModeToggleEl = document.getElementById('debugModeToggle');

			if (debugModeToggleEl) debugModeToggleEl.checked = isDebugMode();

			const modbusConfig = (currentConfig.tools_config && currentConfig.tools_config.modbus) || {};
			const modbusToolEnabledEl = document.getElementById('modbusToolEnabled');
			const modbusWriteEnabledEl = document.getElementById('modbusWriteEnabled');
			const modbusTargetCountEl = document.getElementById('modbusTargetCount');

			if (modbusToolEnabledEl) modbusToolEnabledEl.checked = !!modbusConfig.enabled;
			if (modbusWriteEnabledEl) modbusWriteEnabledEl.checked = !!modbusConfig.allow_write;
			if (modbusTargetCountEl) modbusTargetCountEl.textContent = String(Object.keys(modbusConfig.targets || {}).length);
			syncModbusSettingsState(false);

			// Gateway配置

			const gateway = currentConfig.gateway || {};

			const gatewayEnabledEl = document.getElementById('gatewayEnabled');

			if (gatewayEnabledEl) gatewayEnabledEl.checked = gateway.enabled || false;

			const gatewayListenAddrEl = document.getElementById('gatewayListenAddr');

			if (gatewayListenAddrEl) gatewayListenAddrEl.value = gateway.listen_addr || ':8080';

			const gatewayMaxTasksEl = document.getElementById('gatewayMaxTasks');

			if (gatewayMaxTasksEl) gatewayMaxTasksEl.value = gateway.max_tasks || 10;

			const gatewayTaskTimeoutEl = document.getElementById('gatewayTaskTimeout');

			if (gatewayTaskTimeoutEl) gatewayTaskTimeoutEl.value = gateway.task_timeout || 300;

			const gatewayEnableWebUIEl = document.getElementById('gatewayEnableWebUI');

			if (gatewayEnableWebUIEl) gatewayEnableWebUIEl.checked = gateway.enable_webui || false;

			const gatewayWebUIPathEl = document.getElementById('gatewayWebUIPath');

			if (gatewayWebUIPathEl) gatewayWebUIPathEl.value = gateway.webui_path || './webui';

			// 邮件配置

			const email = currentConfig.email_config || {};

			const emailEnabledEl = document.getElementById('emailEnabled');

			if (emailEnabledEl) emailEnabledEl.checked = email.enabled || false;

			const smtpHostEl = document.getElementById('smtpHost');

			if (smtpHostEl) smtpHostEl.value = email.smtp_host || '';

			const smtpPortEl = document.getElementById('smtpPort');

			if (smtpPortEl) smtpPortEl.value = email.smtp_port || 465;

			const imapHostEl = document.getElementById('imapHost');

			if (imapHostEl) imapHostEl.value = email.imap_host || '';

			const imapPortEl = document.getElementById('imapPort');

			if (imapPortEl) imapPortEl.value = email.imap_port || 993;

			const emailUsernameEl = document.getElementById('emailUsername');

			if (emailUsernameEl) emailUsernameEl.value = email.username || '';

			const emailPasswordEl = document.getElementById('emailPassword');

			if (emailPasswordEl) emailPasswordEl.value = email.password || '';

			const fromAddressEl = document.getElementById('fromAddress');

			if (fromAddressEl) fromAddressEl.value = email.from_address || '';

			const useTLSEl = document.getElementById('useTLS');

			if (useTLSEl) useTLSEl.checked = email.use_tls || false;

			// Agents列表

			renderAgentsList();

			// 模型列表

			renderModelsList();

			// 渠道列表

			renderChannelsList();

			addLog('info', '配置加载成功');

		} catch (err) {

			console.error('加载配置失败:', err);

			addLog('error', '加载配置失败: ' + err.message);

			alert('加载配置失败:[可能是kitty已经退出网页失去响应 或 ] ' + err.message);

		}

	}



	function collectConfigDataLegacy() {

		// 收集模型配置

		let modelConfig = {};

		const modelItems = document.querySelectorAll('.model-config-item');
		if (modelItems.length === 0 && currentConfig && currentConfig.model_config) {
			modelConfig = JSON.parse(JSON.stringify(currentConfig.model_config));
		}

		for (let i = 0; i < modelItems.length; i++) {

			const item = modelItems[i];

			const keyInput = item.querySelector('.model-key');

			const modelKey = keyInput ? keyInput.value : '';

			if (modelKey) {

				const typeInput = item.querySelector('.model-type');

				const apiKeyInput = item.querySelector('.model-api-key');

				const baseUrlInput = item.querySelector('.model-base-url');

				const modelNameInput = item.querySelector('.model-model-name');

				const tempInput = item.querySelector('.model-temperature');

				const maxTokensInput = item.querySelector('.model-max-tokens');

				modelConfig[modelKey] = {

					type: typeInput ? typeInput.value : '',

					api_key: apiKeyInput ? apiKeyInput.value : '',

					base_url: baseUrlInput ? baseUrlInput.value : '',

					model_name: modelNameInput ? modelNameInput.value : '',

					temperature: parseFloat(tempInput ? tempInput.value : 0.7),

					max_tokens: parseInt(maxTokensInput ? maxTokensInput.value : 4096)

				};

			}

		}

		// 收集 Agents 配置

		const agents = [];

		const agentItems = document.querySelectorAll('.agent-config-item');

		for (let i = 0; i < agentItems.length; i++) {

			const item = agentItems[i];

			const nameInput = item.querySelector('.agent-name');

			const typeInput = item.querySelector('.agent-type');

			const modelInput = item.querySelector('.agent-model');

			const systemMsgInput = item.querySelector('.agent-system-message');

			const tempInput = item.querySelector('.agent-temperature');

			const maxTokensInput = item.querySelector('.agent-max-tokens');

			const hasMemoryCheckbox = item.querySelector('.agent-has-memory');

			const memoryWindowInput = item.querySelector('.agent-memory-window');

			if (nameInput && nameInput.value) {

				agents.push({

					name: nameInput.value,

					type: typeInput ? typeInput.value : 'custom',

					model: modelInput ? modelInput.value : 'default_model',

					system_message: systemMsgInput ? systemMsgInput.value : '',

					temperature: parseFloat(tempInput ? tempInput.value : 0.7),

					max_tokens: parseInt(maxTokensInput ? maxTokensInput.value : 4096),

					has_memory: hasMemoryCheckbox ? hasMemoryCheckbox.checked : true,

					memory_window: parseInt(memoryWindowInput ? memoryWindowInput.value : 50)

				});

			}

		}

		// 收集渠道配置
		var CHANNEL_FIELD_KEYS = {
			feishu: ['app_id', 'app_secret'],
			qq: ['app_id', 'app_secret'],
			dingtalk: ['client_id', 'client_secret', 'robot_code'],
			wework: ['bot_id', 'secret'],
			weixin: ['account_id', 'token', 'base_url', 'bot_agent', 'allowed_users'],
			email: ['smtp_host', 'smtp_port', 'imap_host', 'imap_port', 'username', 'password', 'from_address', 'from_name', 'supervisor', 'use_tls']
		};
		var channels = [];
		var channelItems = document.querySelectorAll('.channel-config-item');
		for (var i = 0; i < channelItems.length; i++) {
			var item = channelItems[i];
			var nameInput = item.querySelector('.channel-name');
			var typeSelect = item.querySelector('.channel-type');
			var enabledCheckbox = item.querySelector('.channel-enabled');
			if (!nameInput || !nameInput.value) continue;
			var chType = typeSelect ? typeSelect.value : '';
			var channelConfig = {};
			var fieldKeys = CHANNEL_FIELD_KEYS[chType];
			if (fieldKeys) {
				for (var k = 0; k < fieldKeys.length; k++) {
					var fk = fieldKeys[k];
					var fInput = item.querySelector('.channel-field-' + fk);
					if (!fInput) continue;
					if (fInput.type === 'checkbox') { channelConfig[fk] = fInput.checked; }
					else { var fVal = fInput.value.trim(); if (fVal) { channelConfig[fk] = (fk === 'smtp_port' || fk === 'imap_port') ? (parseInt(fVal,10)||fVal) : fVal; } }
				}
			} else {
				var configTextarea = item.querySelector('.channel-config');
				if (configTextarea && configTextarea.value) { try { channelConfig = JSON.parse(configTextarea.value); } catch(e){} }
			}
			channels.push({ name: nameInput.value, type: chType, enabled: enabledCheckbox ? enabledCheckbox.checked : false, config: channelConfig });
		}

		const configData = {

			team_name: document.getElementById('teamName')?.value || '',

			workspace: document.getElementById('workspace')?.value || '',

			headless: document.getElementById('headless')?.checked || false,

			output: {

				format: document.getElementById('outputFormat')?.value || 'json',

				dir: document.getElementById('outputDir')?.value || 'storage'

			},

			runtime: {

				semaphore_limit: parseInt(document.getElementById('semaphoreLimit')?.value) || 4

			},

			gateway: {

				enabled: document.getElementById('gatewayEnabled')?.checked || false,

				listen_addr: document.getElementById('gatewayListenAddr')?.value || ':8080',

				max_tasks: parseInt(document.getElementById('gatewayMaxTasks')?.value) || 10,

				task_timeout: parseInt(document.getElementById('gatewayTaskTimeout')?.value) || 300,

				enable_webui: document.getElementById('gatewayEnableWebUI')?.checked || false,

				webui_path: document.getElementById('gatewayWebUIPath')?.value || './webui'

			},

			tools_config: {

				enabled: !(currentConfig && currentConfig.tools_config && currentConfig.tools_config.enabled === false),

				disabled_tools: collectDisabledToolsFromSettings(),

				private_network_protection: document.getElementById('privateNetworkProtection')?.checked || false,

				modbus: collectModbusConfigFromSettings()

			},

			email_config: {

				enabled: document.getElementById('emailEnabled')?.checked || false,

				smtp_host: document.getElementById('smtpHost')?.value || '',

				smtp_port: parseInt(document.getElementById('smtpPort')?.value) || 465,

				imap_host: document.getElementById('imapHost')?.value || '',

				imap_port: parseInt(document.getElementById('imapPort')?.value) || 993,

				username: document.getElementById('emailUsername')?.value || '',

				password: document.getElementById('emailPassword')?.value || '',

				from_address: document.getElementById('fromAddress')?.value || '',

				use_tls: document.getElementById('useTLS')?.checked || false

			},

			agents: agents,

			model_config: modelConfig,

			channels: channels

		};

		return configData;

	}

	function settingModelRequiresAPIKey(model) {
		if (!model) return true;
		if (/\/api\/macs\/v1\/?$/.test(model.base_url || '') && !String(model.api_key || '').trim() && !String(model.api_key_env || '').trim()) return false;
		var value = [model.type, model.base_url].join(' ').toLowerCase();
		return !(value.includes('ollama') || value.includes('vllm') || value.includes('localhost') || value.includes('127.0.0.1'));
	}

	function settingSuspiciousAPIKey(key) {
		key = String(key || '').trim();
		if (!key) return true;
		var lower = key.toLowerCase();
		var suspicious = ['your_', 'your-', 'sk-your', 'sk-xxx', 'placeholder', 'change_me', 'changeme', 'api_key_here', 'undefined', 'null'];
		for (var i = 0; i < suspicious.length; i++) {
			if (lower.indexOf(suspicious[i]) >= 0) return true;
		}
		return key.length < 10;
	}

	function settingModelUnavailableReason(model) {
		if (!model || !String(model.model_name || '').trim()) return 'model_name missing';
		if (settingModelRequiresAPIKey(model) && settingSuspiciousAPIKey(model.api_key)) return 'api_key missing or placeholder';
		return '';
	}

	function settingAvailableModelNames(modelConfig) {
		var models = modelConfig || {};
		var names = Object.keys(models).filter(function(name) { return !settingModelUnavailableReason(models[name]); });
		return sortModelNamesAssistantFirst(names, currentConfig || {});
	}

	function buildAgentModelSelect(agent, index) {
		var models = currentConfig?.model_config || {};
		var current = agent.model || '';
		var names = sortModelNamesAssistantFirst(Object.keys(models), currentConfig || {});
		var html = '<select class="agent-model" data-index="' + index + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">';
		html += '<option value=""' + (!current ? ' selected' : '') + '>' + escapeHtml(settingsText('settingsInheritModel', '跟随当前选择的模型')) + '</option>';
		if (current && !models[current]) {
			html += '<option value="' + escapeHtml(current) + '" selected>' + escapeHtml(current) + ' / ' + escapeHtml(settingsText('settingsModelMissing', '模型不存在')) + '</option>';
		}
		for (var i = 0; i < names.length; i++) {
			var name = names[i];
			var reason = settingModelUnavailableReason(models[name]);
			var label = name + (reason ? ' / ' + settingsText('settingsModelNotReady', '未就绪') + ': ' + settingsHumanModelReason(reason) : '');
			html += '<option value="' + escapeHtml(name) + '"' + (name === current ? ' selected' : '') + '>' + escapeHtml(label) + '</option>';
		}
		html += '</select>';
		if (names.length === 0) {
			html += '<div style="margin-top:4px;color:#b91c1c;font-size:calc(12.1px * var(--macs-font-scale, 1));">' + escapeHtml(settingsText('settingsAddModelFirst', '请先到模型配置添加可用模型')) + '</div>';
		}
		return html;
	}

	function validateSettingsConfigData(configData) {
		var errors = [];
		var warnings = [];
		var modelConfig = configData.model_config || {};
		var available = settingAvailableModelNames(modelConfig);
		var availableMap = {};
		available.forEach(function(name) { availableMap[name] = true; });
		if (available.length === 0) {
			errors.push(settingsText('configHealthNoUsableModel', '没有可用模型：请先在「模型配置」里配置至少一个真实可用的模型。'));
		}
		var defaultModel = settingsSelectedModel(configData);
		if (configData.default_model && !availableMap[defaultModel]) errors.push(settingsText('settingsCurrentModelUnavailable', '当前选择的模型不可用，请重新选择。'));
		(configData.agents || []).forEach(function(agent) {
			var effectiveModel = agent.model || defaultModel;
			if (!effectiveModel) {
				errors.push(settingsTemplate('configHealthAgentNoModel', 'Agent {agent} 未选择模型。', {agent: agent.name}));
			} else if (!modelConfig[effectiveModel]) {
				errors.push(settingsTemplate('configHealthAgentMissingModel', 'Agent {agent} 选择的模型 {model} 不存在。', {agent: agent.name, model: agent.model}));
			} else if (!availableMap[effectiveModel]) {
				errors.push(settingsTemplate('configHealthAgentUnavailable', 'Agent {agent} 选择的模型 {model} 不可用：{reason}', {
					agent: agent.name,
					model: effectiveModel,
						reason: settingsHumanModelReason(settingModelUnavailableReason(modelConfig[effectiveModel]))
				}));
			}
		});
		(configData.channels || []).forEach(function(channel) {
			if (!channel.enabled) return;
			var required = {feishu:['app_id','app_secret'], dingtalk:['client_id','client_secret'], qq:['app_id','app_secret'], wework:['bot_id','secret'], weixin:['account_id','token']}[String(channel.type || '').toLowerCase()];
			if (!required) return;
			var missing = required.filter(function(k) { return !String((channel.config || {})[k] || '').trim(); });
			if (missing.length) warnings.push(settingsTemplate('configHealthChannelMissing', '渠道 {channel} ({type}) 已启用但缺少参数：{fields}', {
				channel: channel.name,
				type: channel.type,
				fields: missing.join(', ')
			}));
		});
		return {ok: errors.length === 0, errors: errors, warnings: warnings};
	}

	function renderConfigHealth(status) {
        var box = document.getElementById('configHealthBox');
        if (!box) return;
        if (!status) { box.style.display = 'none'; return; }
        var issues = (status.errors || []).concat(status.warnings || []);
        var ok = !!status.ok;
        var title = settingsText(ok ? 'settingsReady' : 'settingsNeedsAttention', ok ? '已具备开始对话的配置' : '还有设置需要完善');
        box.innerHTML = '<div class="settings-health ' + (ok ? 'ready' : 'attention') + '"><strong>' + escapeHtml(title) + '</strong>' +
            (issues.length ? '<details><summary>' + escapeHtml(settingsText('settingsViewIssues', '查看需要处理的事项')) + '</summary><ul>' + issues.map(function(issue) { return '<li>' + escapeHtml(issue) + '</li>'; }).join('') + '</ul></details>' : '<span>' + escapeHtml(settingsText('settingsHealthHint', '配置检查通过。实际连接状态请使用“测试连接”确认。')) + '</span>') + '</div>';
        box.style.display = 'block';
    }


	function renderAgentsListLegacy() {

		var container = document.getElementById('agentsList');

		if (!container) return;

		var agents = currentConfig?.agents || [];

		if (agents.length === 0) {

			container.innerHTML = '<div style="text-align:center; color:#adb5bd; padding:20px;">暂无Agent配置</div>';

			return;

		}

		var html = '';

		for (var i = 0; i < agents.length; i++) {

			var agent = agents[i];

			html += '<div class="agent-config-item" style="border: 1px solid #e9ecef; border-radius: 8px; padding: 12px; margin-bottom: 10px;">' +

				'<div style="display: flex; justify-content: space-between; margin-bottom: 10px;">' +

				'<strong>' + escapeHtml(agent.name || '未命名') + '</strong>' +

				'<button type="button" onclick="removeAgent(' + i + ')" style="background: #dc3545; color: white; border: none; border-radius: 4px; padding: 2px 8px; cursor: pointer;">删除</button>' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">名称</label>' +

				'<input type="text" class="agent-name" value="' + escapeHtml(agent.name || '') + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">类型</label>' +

				'<input type="text" class="agent-type" value="' + escapeHtml(agent.type || 'custom') + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">模型</label>' +

				buildAgentModelSelect(agent, i) +
				'<span onclick="switchConfigTab(&quot;models&quot;)" style="cursor:pointer;color:#6366f1;font-size:calc(12.1px * var(--macs-font-scale, 1));margin-top:3px;display:inline-block;">⚙️ 配置模型</span>' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">系统消息</label>' +

				'<textarea class="agent-system-message" data-index="' + i + '" rows="2" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' + escapeHtml(agent.system_message || '') + '</textarea>' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">温度 (0-1)</label>' +

				'<input type="number" step="0.1" class="agent-temperature" value="' + (agent.temperature || 0.7) + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">最大Token</label>' +

				'<input type="number" class="agent-max-tokens" value="' + (agent.max_tokens || 4096) + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">' +

				'<input type="checkbox" class="agent-has-memory" data-index="' + i + '" ' + (agent.has_memory !== false ? 'checked' : '') + '>' +

				' 启用记忆</label>' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">记忆窗口大小</label>' +

				'<input type="number" class="agent-memory-window" value="' + (agent.memory_window || 50) + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'</div>';

		}

		container.innerHTML = html;

	}

	function renderModelsListLegacy() {

		var container = document.getElementById('modelsList');

		if (!container) return;

		var models = currentConfig?.model_config || {};

		var modelNames = Object.keys(models);

		if (modelNames.length === 0) {

			container.innerHTML = '<div style="text-align:center; color:#adb5bd; padding:20px;">暂无模型配置</div>';

			return;

		}

		var html = '';

		for (var i = 0; i < modelNames.length; i++) {

			var name = modelNames[i];

			var model = models[name];

			html += '<div class="model-config-item" style="border: 1px solid #e9ecef; border-radius: 8px; padding: 12px; margin-bottom: 10px;">' +

				'<div style="display: flex; justify-content: space-between; margin-bottom: 10px;">' +

				'<strong>' + escapeHtml(name) + '</strong>' +

				'<button type="button" onclick="removeModel(\'' + escapeHtml(name) + '\')" style="background: #dc3545; color: white; border: none; border-radius: 4px; padding: 2px 8px; cursor: pointer;">删除</button>' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">模型标识</label>' +

				'<input type="text" class="model-key" value="' + escapeHtml(name) + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">类型 (deepseek/openai/ollama)</label>' +

				'<input type="text" class="model-type" value="' + escapeHtml(model.type || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">API Key</label>' +

				'<input type="text" class="model-api-key" value="' + escapeHtml(model.api_key || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">Base URL</label>' +

				'<input type="text" class="model-base-url" value="' + escapeHtml(model.base_url || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">模型名称</label>' +

				'<input type="text" class="model-model-name" value="' + escapeHtml(model.model_name || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">温度</label>' +

				'<input type="number" step="0.1" class="model-temperature" value="' + (model.temperature || 0.7) + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'<div class="config-field" style="margin-bottom: 8px;">' +

				'<label style="display:block; font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-bottom:4px;">最大Token</label>' +

				'<input type="number" class="model-max-tokens" value="' + (model.max_tokens || 4096) + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +

				'</div>' +

				'</div>';

		}

		html += '<div style="margin-top:12px;"><span onclick="switchConfigTab(&quot;agents&quot;)" style="cursor:pointer;color:#6366f1;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:600;">→ 去配Agent</span> <span style="color:#94a3b8;font-size:calc(12.1px * var(--macs-font-scale, 1));">（在Agent中选择此模型名）</span></div>';
		container.innerHTML = html;

	}





	// 渲染渠道列表

	var CHANNEL_SCHEMAS = {
		feishu:{title:'飞书',icon:'🐦',helpText:'访问 open.feishu.cn → 创建应用 → 左侧「凭证与基础信息」获取 App ID 和 App Secret',
			fields:[{key:'app_id',label:'App ID',placeholder:'例：cli_a94cc9c965785cd5',type:'text',help:'在飞书开放平台「应用信息」页获取'},
				{key:'app_secret',label:'App Secret',placeholder:'例：pGJW1oZYC7yO4RWdYgsfKftX7wuC2AyB',type:'password',help:'在飞书开放平台「应用信息」页获取'}]},
		qq:{title:'QQ',icon:'🐧',helpText:'访问 q.qq.com → 创建机器人 → 获取 App ID 和 App Secret',
			fields:[{key:'app_id',label:'App ID',placeholder:'例：1903702067',type:'text',help:'QQ 开放平台「机器人管理」页面获取'},
				{key:'app_secret',label:'App Secret',placeholder:'例：YOUR_QQ_APP_SECRET',type:'password',help:'QQ 开放平台「机器人管理」页面获取'}]},
		dingtalk:{title:'钉钉',icon:'📱',helpText:'访问 open.dingtalk.com → 创建应用 → 获取 Client ID 和 Client Secret',
			fields:[{key:'client_id',label:'Client ID',placeholder:'例：dingl3oti9dqisxtkl7p',type:'text',help:'钉钉开放平台「应用信息」页面获取'},
				{key:'client_secret',label:'Client Secret',placeholder:'例：YOUR_DINGTALK_CLIENT_SECRET',type:'password',help:'钉钉开放平台「应用信息」页面获取'},
				{key:'robot_code',label:'Robot Code',placeholder:'留空或用默认值 nouse',type:'text',help:'机器人编码，可留空'}]},
		wework:{title:'企业微信',icon:'💼',helpText:'访问 work.weixin.qq.com → 应用管理 → 创建机器人 → 获取 Bot ID 和 Secret',
			fields:[{key:'bot_id',label:'Bot ID',placeholder:'例：aib07pMeraKR4aeNndVleygYoZAirZQzJqa',type:'text',help:'企业微信管理后台「机器人」页面获取'},
				{key:'secret',label:'Secret',placeholder:'例：YOUR_WEWORK_SECRET',type:'password',help:'企业微信管理后台「机器人」页面获取'}]},
		weixin:{title:'个人微信 iLink',icon:'微信',helpText:'使用 Tencent OpenClaw Weixin / iLink Bot API 扫码连接，不使用 PC Hook 或非官方协议',
			fields:[{key:'account_id',label:'Account ID',placeholder:'扫码后自动填写',type:'text'},
				{key:'token',label:'Token',placeholder:'扫码后自动填写',type:'password'},
				{key:'base_url',label:'Base URL',placeholder:'https://ilinkai.weixin.qq.com',type:'text'},
				{key:'bot_agent',label:'Bot Agent',placeholder:'MACS/1.0 OpenClawCompatible/2.4.6',type:'text'},
				{key:'allowed_users',label:'允许用户',placeholder:'可选，多个微信ID用逗号分隔',type:'text'}]},
		email:{title:'邮箱',icon:'📧',helpText:'需先开启邮箱 POP3/SMTP/IMAP。QQ邮箱：设置→账户→POP3/SMTP服务→获取授权码。163邮箱：设置→POP3/SMTP/IMAP→开启',
			fields:[{key:'smtp_host',label:'SMTP 服务器',placeholder:'例：smtp.163.com',type:'text'},
				{key:'smtp_port',label:'SMTP 端口',placeholder:'例：465（163）/ 587（QQ）',type:'text'},
				{key:'imap_host',label:'IMAP 服务器',placeholder:'例：imap.163.com',type:'text'},
				{key:'imap_port',label:'IMAP 端口',placeholder:'例：993',type:'text'},
				{key:'username',label:'邮箱地址',placeholder:'例：yourname@163.com',type:'text'},
				{key:'password',label:'授权码/密码',placeholder:'邮箱设置中获取的授权码',type:'password'},
				{key:'from_address',label:'发件地址',placeholder:'同邮箱地址即可',type:'text'},
				{key:'from_name',label:'发件人名称',placeholder:'例：AI助手',type:'text'},
				{key:'supervisor',label:'管理员邮箱',placeholder:'例：admin@example.com',type:'text'},
				{key:'use_tls',label:'使用 TLS',type:'checkbox'}]}
	};
	function renderChannelsListLegacy() {
		var container = document.getElementById('channelsList');
		if (!container) { console.error('channelsList missing'); return; }
		if (!currentConfig) { container.innerHTML = '<div style="text-align:center;color:#adb5bd;padding:20px;">配置未加载</div>'; return; }


		var channels = currentConfig.channels || [];
		if (channels.length === 0) { container.innerHTML = '<div style="text-align:center;color:#adb5bd;padding:20px;">暂无渠道配置，请添加</div>'; return; }

		var html = '';
		for (var i = 0; i < channels.length; i++) {
			var channel = channels[i], chType = channel.type || '', schema = CHANNEL_SCHEMAS[chType], hasSchema = !!schema;
			html += '<div class="channel-config-item" style="border:1px solid #e9ecef;border-radius:8px;padding:12px;margin-bottom:10px;">';
			html += '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;"><strong>' + (hasSchema?schema.icon+' ':'') + escapeHtml(channel.name||'未命名') + ' (' + escapeHtml(chType||'自定义') + ')</strong><button type="button" onclick="removeChannel('+i+')" style="background:#dc3545;color:white;border:none;border-radius:4px;padding:2px 8px;cursor:pointer;">删除</button></div>';
			html += '<div class="config-field" style="margin-bottom:8px;"><label style="display:block;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6c757d;margin-bottom:4px;">名称</label><input type="text" class="channel-name" value="'+escapeHtml(channel.name||'')+'" data-index="'+i+'" style="width:100%;padding:6px;border:1px solid #dee2e6;border-radius:4px;"></div>';
			html += '<div class="config-field" style="margin-bottom:8px;"><label style="display:block;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6c757d;margin-bottom:4px;">类型</label><select class="channel-type" data-index="'+i+'" onchange="onChannelTypeChange('+i+')" style="width:100%;padding:6px;border:1px solid #dee2e6;border-radius:4px;"><option value="">自定义（JSON 配置）</option><option value="feishu"'+(chType==='feishu'?' selected':'')+'>🐦 飞书 (feishu)</option><option value="qq"'+(chType==='qq'?' selected':'')+'>🐧 QQ (qq)</option><option value="dingtalk"'+(chType==='dingtalk'?' selected':'')+'>📱 钉钉 (dingtalk)</option><option value="wework"'+(chType==='wework'?' selected':'')+'>💼 企业微信 (wework)</option><option value="weixin"'+(chType==='weixin'?' selected':'')+'>微信 iLink (weixin)</option><option value="email"'+(chType==='email'?' selected':'')+'>📧 邮箱 (email)</option></select></div>';
			html += '<div class="config-field" style="margin-bottom:8px;"><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6c757d;"><input type="checkbox" class="channel-enabled" data-index="'+i+'" '+(channel.enabled?'checked':'')+'> 启用</label></div>';
			if (hasSchema && schema.helpText) html += '<div style="background:#E3F2FD;padding:8px 10px;border-radius:6px;margin-bottom:10px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#1976D2;">💡 '+escapeHtml(schema.helpText)+'</div>';
			if (hasSchema) {
				var config = channel.config || {};
				for (var j = 0; j < schema.fields.length; j++) {
					var field = schema.fields[j], val = config[field.key] !== undefined ? config[field.key] : '', fieldId = 'ch_field_'+i+'_'+field.key;
					html += '<div class="config-field" style="margin-bottom:6px;display:flex;align-items:center;gap:6px;"><label for="'+fieldId+'" style="flex:0 0 110px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6c757d;text-align:right;">'+escapeHtml(field.label)+'</label>';
					if (field.type === 'checkbox') html += '<input type="checkbox" class="channel-field-'+field.key+'" data-index="'+i+'" id="'+fieldId+'" '+(val?'checked':'')+'>';
					else html += '<input type="'+field.type+'" class="channel-field-'+field.key+'" data-index="'+i+'" id="'+fieldId+'" value="'+escapeHtml(String(val))+'" placeholder="'+escapeHtml(field.placeholder||'')+'" style="flex:1;padding:6px;border:1px solid #dee2e6;border-radius:4px;font-size:calc(13.2px * var(--macs-font-scale, 1));">';
					if (field.help) html += '<span title="'+escapeHtml(field.help)+'" style="cursor:help;font-size:calc(15.4px * var(--macs-font-scale, 1));opacity:0.6;">❓</span>';
					html += '</div>';
				}
				if (chType === 'weixin' || chType === 'openclaw-weixin') {
					html += '<div style="margin-top:10px;border-top:1px solid #e5e7eb;padding-top:10px;">' +
						'<button type="button" onclick="startWeixinQrLogin('+i+')" style="padding:6px 12px;background:#16a34a;color:white;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">扫码登录微信</button>' +
						'<span id="weixinQrStatus_'+i+'" style="margin-left:10px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;"></span>' +
						'<div id="weixinQrBox_'+i+'" style="margin-top:10px;"></div>' +
						'</div>';
				}
			} else {
				var configJson = JSON.stringify(channel.config || {}, null, 2);
				html += '<div class="config-field" style="margin-bottom:8px;"><label style="display:block;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6c757d;margin-bottom:4px;">配置 (JSON)</label><textarea class="channel-config" data-index="'+i+'" rows="6" style="width:100%;padding:6px;border:1px solid #dee2e6;border-radius:4px;font-family:monospace;font-size:calc(12.1px * var(--macs-font-scale, 1));">'+escapeHtml(configJson)+'</textarea></div>';
			}
			html += '</div>';
		}
		container.innerHTML = html;
	}



	


	function addAgentLegacy() {

		if (!currentConfig.agents) {

			currentConfig.agents = [];

		}

		var firstAvailableModel = settingAvailableModelNames(currentConfig.model_config || {})[0] || '';
		currentConfig.agents.push({

			name: "new_agent",

			type: "",

			model: firstAvailableModel,

			system_message: "You are a helpful assistant.",

			temperature: 0.7,

			max_tokens: 4096

		});

		renderAgentsList();

	}



	// 移除Agent

	function removeAgentLegacy(index) {

		if (confirm('确定要删除这个Agent吗？')) {

			currentConfig.agents.splice(index, 1);

			renderAgentsList();

		}

	}



	// 添加模型

	function addModelLegacy() {

		if (!currentConfig.model_config) {

			currentConfig.model_config = {};

		}

		const newModelName = 'new_model_' + Date.now();

		currentConfig.model_config[newModelName] = {

			type: "deepseek",

			api_key: "",

			base_url: "https://api.deepseek.com/v1",

			model_name: "deepseek-chat",

			temperature: 0.7,

			max_tokens: 4096

		};

		renderModelsList();
		renderAgentsList();

	}



	// 移除模型

	function removeModelLegacy(name) {

		if (confirm('确定要删除这个模型配置吗？')) {

			delete currentConfig.model_config[name];

			renderModelsList();
			renderAgentsList();

		}

	}



	// 添加渠道

	async function startWeixinQrLoginLegacy(index) {
		var nameEl = document.querySelector('.channel-name[data-index="'+index+'"]');
		var channelName = nameEl && nameEl.value.trim() ? nameEl.value.trim() : 'weixin';
		var statusEl = document.getElementById('weixinQrStatus_' + index);
		var boxEl = document.getElementById('weixinQrBox_' + index);
		if (statusEl) statusEl.textContent = '正在生成二维码...';
		if (boxEl) boxEl.innerHTML = '';
		try {
			var response = await fetch('/api/weixin/login/start', {
				method: 'POST',
				headers: {'Content-Type': 'application/json'},
				body: JSON.stringify({channel: channelName, force: true})
			});
			var data = await response.json();
			if (!response.ok || !data.ok || !data.qr_data_url) {
				throw new Error((data && (data.error || data.message)) || '二维码生成失败');
			}
			if (statusEl) statusEl.textContent = '请用手机微信扫码确认';
			if (boxEl) {
				var qr = String(data.qr_data_url || '');
				var qrHtml = '<div style="display:flex;align-items:flex-start;gap:12px;">';
				qrHtml += '<div>';
				qrHtml += '<img id="weixinQrImage_'+index+'" src="'+escapeHtml(qr)+'" alt="Weixin QR" style="width:180px;height:180px;border:1px solid #e5e7eb;border-radius:8px;background:white;object-fit:contain;">';
				qrHtml += '<div id="weixinQrImageError_'+index+'" style="display:none;width:180px;min-height:120px;border:1px dashed #fca5a5;border-radius:8px;padding:12px;color:#b91c1c;background:#fff5f5;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">二维码图片加载失败。请重新点击扫码连接，或查看后台日志里的 /api/weixin/login/start 返回。</div>';
				qrHtml += '</div>';
				qrHtml += '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#475569;line-height:1.7;">扫码后本机将保存 iLink Bot token 到渠道配置。<br>建议保留 allowed_users，只允许扫码用户触发 AI 员工。</div>';
				qrHtml += '</div>';
				boxEl.innerHTML = qrHtml;
			}
			pollWeixinQrLogin(data.session_key, channelName, index);
		} catch (err) {
			if (statusEl) statusEl.textContent = '扫码启动失败: ' + err.message;
			if (boxEl) boxEl.innerHTML = '';
		}
	}

	async function pollWeixinQrLogin(sessionKey, channelName, index) {
		var statusEl = document.getElementById('weixinQrStatus_' + index);
		var deadline = Date.now() + 8 * 60 * 1000;
		var verifyCode = '';
		while (Date.now() < deadline) {
			try {
				var response = await fetch('/api/weixin/login/wait', {
					method: 'POST',
					headers: {'Content-Type': 'application/json'},
					body: JSON.stringify({session_key: sessionKey, channel: channelName, verify_code: verifyCode})
				});
				verifyCode = '';
				var data = await response.json();
				if (!response.ok || data.error) {
					throw new Error(data.error || data.message || '扫码状态查询失败');
				}
				if (data.connected) {
					if (statusEl) statusEl.textContent = '微信已连接，配置已保存';
					await loadConfigToForm();
					return;
				}
				if (data.already_connected) {
					if (statusEl) statusEl.textContent = '该微信 Bot 已连接';
					await loadConfigToForm();
					return;
				}
				if (data.status === 'need_verifycode') {
					var code = prompt('请输入手机微信上显示的数字验证码');
					if (code) verifyCode = code.trim();
				}
				if (data.status === 'expired' || data.status === 'verify_code_blocked' || data.status === 'missing') {
					if (statusEl) statusEl.textContent = data.message || '二维码已失效，请重新生成';
					return;
				}
				if (statusEl) statusEl.textContent = data.message || ('等待扫码: ' + (data.status || 'wait'));
			} catch (err) {
				if (statusEl) statusEl.textContent = '扫码轮询失败: ' + err.message;
				return;
			}
		}
		if (statusEl) statusEl.textContent = '扫码超时，请重新生成二维码';
	}

	function addChannelLegacy() {
		openChannelEditor(-1);
	}

	function findWeixinChannelIndex() {
		var channels = currentConfig && currentConfig.channels ? currentConfig.channels : [];
		for (var i = 0; i < channels.length; i++) {
			var t = String(channels[i].type || '').toLowerCase();
			if (t === 'weixin' || t === 'openclaw-weixin') return i;
		}
		return -1;
	}

	function addWeixinChannelAndLoginLegacy() {
		if (!currentConfig.channels) currentConfig.channels = [];
		var idx = findWeixinChannelIndex();
		if (idx < 0) {
			openChannelEditor(-1, {
				name: 'weixin',
				type: 'weixin',
				enabled: true,
				config: settingDefaultChannelConfig('weixin')
			});
			return;
		}
		setTimeout(function() {
			var item = document.querySelector('.channel-config-item:nth-child(' + (idx + 1) + ')');
			if (item && item.scrollIntoView) item.scrollIntoView({behavior: 'smooth', block: 'center'});
			startWeixinQrLogin(idx);
		}, 50);
	}
		// 保存渠道配置
		function saveChannelsConfigLegacy() {
			var channels = currentConfig.channels || [];
			for (var i = 0; i < channels.length; i++) {
				var nameEl = document.querySelector('.channel-name[data-index="'+i+'"]');
				var typeEl = document.querySelector('.channel-type[data-index="'+i+'"]');
				var enabledEl = document.querySelector('.channel-enabled[data-index="'+i+'"]');
				if (nameEl) channels[i].name = nameEl.value;
				if (typeEl) channels[i].type = typeEl.value;
				if (enabledEl) channels[i].enabled = !!enabledEl.checked;
				if (CHANNEL_SCHEMAS[channels[i].type||'']) {
					var schema = CHANNEL_SCHEMAS[channels[i].type||''];
					var config = channels[i].config || {};
					for (var j = 0; j < schema.fields.length; j++) {
						var field = schema.fields[j];
						var input = document.querySelector('.channel-field-'+field.key+'[data-index="'+i+'"]');
						if (input) config[field.key] = input.type === 'checkbox' ? input.checked : input.value;
					}
					channels[i].config = config;
				} else {
					var cfgEl = document.querySelector('.channel-config[data-index="'+i+'"]');
					if (cfgEl) { try { channels[i].config = JSON.parse(cfgEl.value); } catch(e) {} }
				}
			}
			saveConfig();
		}



	// 渠道类型切换时重新渲染
	function onChannelTypeChangeLegacy(index) {
		var select = document.querySelector('.channel-type[data-index="' + index + '"]');
		if (select) {
			currentConfig.channels[index].type = select.value;
			currentConfig.channels[index].config = {};
			renderChannelsList();
		}
	}

	// 移除渠道

	function removeChannelLegacy(index) {

		if (confirm('确定要删除这个渠道吗？')) {

			currentConfig.channels.splice(index, 1);

			renderChannelsList();

		}

	}

	var cf = document.getElementById('configForm'); if (cf) {
	cf.addEventListener('submit', function(e) {
		e.preventDefault();
		saveConfig();
	});
	cf.addEventListener('input', function(e) {
		if (e.target && e.target.id !== 'debugModeToggle') settingsSetDirty(true);
	});
	cf.addEventListener('change', function(e) {
		if (e.target && e.target.id !== 'debugModeToggle') settingsSetDirty(true);
	});
	} // end if (cf)



	// 导出配置

	async function exportConfig() {

		try {

			const response = await fetch('/api/config/sections');

			const config = await response.json();

			const dataStr = JSON.stringify(config, null, 2);

			const blob = new Blob([dataStr], {type: 'application/json'});

			const url = URL.createObjectURL(blob);

			const a = document.createElement('a');

			a.href = url;

			a.download = 'config_export_' + new Date().toISOString().slice(0,19) + '.json';

			a.click();

			URL.revokeObjectURL(url);

			addLog('info', '配置已导出');

		} catch (err) {

			alert('导出失败: ' + err.message);

		}

	}

	const originalOpenConfigModal = openConfigModal;

	openConfigModal = async function(defaultTab) {

		await loadConfigToForm();

		return originalOpenConfigModal(defaultTab);

	};


// Close dropdowns on outside click
document.addEventListener('click', function(e) {
    if (!e.target.closest('.dropdown')) {
        closeDropdownMenus();
    }
});

// Team panel
window.openTeamPanel = function() {
    var overlay = document.getElementById('teamOverlay');
    if (!overlay) {
        overlay = document.createElement('div');
        overlay.id = 'teamOverlay';
        overlay.className = 'team-overlay';
        overlay.setAttribute('role', 'dialog');
        overlay.setAttribute('aria-modal', 'true');
        overlay.setAttribute('aria-labelledby', 'teamPanelTitle');
        overlay.innerHTML = `<div class="team-modal">
            <div class="team-modal-header">
                <div class="team-modal-title">
                    <span class="ico" data-ico="users"></span>
                    <h2 id="teamPanelTitle" data-i18n="teamsTitle">项目专家团队</h2>
                </div>
                <button class="team-modal-close" onclick="closeTeamPanel()">&times;</button>
            </div>
            <p class="team-modal-sub" data-i18n="teamPanelSubtitle">选择一支专家团队，补充任务说明后开始协作。</p>
            <div class="team-cards">
                <div class="team-card" onclick="useTeamTemplate('research')">
                    <div class="team-card-icon"><span class="ico" data-ico="search"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">研究团队 <span class="team-mode-badge mode-parallel">并行</span></div>
                        <div class="team-card-desc">搜集→分析→撰稿，三角色并行协作，自动合成报告</div>
                        <div class="team-card-roles"><span>研究员</span><span>分析师</span><span>撰稿人</span></div>
                    </div>
                </div>
                <div class="team-card" onclick="useTeamTemplate('coding')">
                    <div class="team-card-icon"><span class="ico" data-ico="wrench"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">编码团队 <span class="team-mode-badge mode-sequential">串行</span></div>
                        <div class="team-card-desc">架构师→程序员→审查员，上游输出驱动下游，最终质量把关</div>
                        <div class="team-card-roles"><span>架构师</span><span>程序员</span><span>审查员</span></div>
                    </div>
                </div>
                <div class="team-card" onclick="useTeamTemplate('fullstack')">
                    <div class="team-card-icon"><span class="ico" data-ico="rocket"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">全栈团队 <span class="team-mode-badge mode-hybrid">混合</span></div>
                        <div class="team-card-desc">PM定义需求→后端+前端并行开发→测试验收→统一交付</div>
                        <div class="team-card-roles"><span>PM</span><span>后端</span><span>前端</span><span>测试</span></div>
                    </div>
                </div>
                <div class="team-card" onclick="useTeamTemplate('content')">
                    <div class="team-card-icon"><span class="ico" data-ico="clipboard"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">内容团队 <span class="team-mode-badge mode-parallel">并行</span></div>
                        <div class="team-card-desc">策划→写作→编辑，三角色并行协作，统一风格交付</div>
                        <div class="team-card-roles"><span>策划</span><span>写手</span><span>编辑</span></div>
                    </div>
                </div>
                <div class="team-card" onclick="useTeamTemplate('ppt')">
                    <div class="team-card-icon"><span class="ico" data-ico="sparkles"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">PPT 工坊 <span class="team-mode-badge mode-sequential">串行</span></div>
                        <div class="team-card-desc">内容策划→设计排版→视觉审核，专业级演示文稿</div>
                        <div class="team-card-roles"><span>内容策划</span><span>设计排版</span><span>视觉审核</span></div>
                    </div>
                </div>
                <div class="team-card" onclick="useTeamTemplate('codebase')">
                    <div class="team-card-icon"><span class="ico" data-ico="search"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">代码审计 <span class="team-mode-badge mode-sequential">串行</span></div>
                        <div class="team-card-desc">项目扫描→文件分析→架构审核，全链路代码质量评估</div>
                        <div class="team-card-roles"><span>项目扫描</span><span>文件分析</span><span>架构审核</span></div>
                    </div>
                </div>
                <div class="team-card" onclick="useTeamTemplate('contract')">
                    <div class="team-card-icon"><span class="ico" data-ico="scroll"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">合同审查 <span class="team-mode-badge mode-sequential">串行</span></div>
                        <div class="team-card-desc">结构扫描→条款分析→风险评估→合规检查</div>
                        <div class="team-card-roles"><span>结构扫描</span><span>条款分析</span><span>合规审核</span></div>
                    </div>
                </div>
                <div class="team-card" onclick="useTeamTemplate('competitor')">
                    <div class="team-card-icon"><span class="ico" data-ico="target"></span></div>
                    <div class="team-card-body">
                        <div class="team-card-name">竞对情报 <span class="team-mode-badge mode-hybrid">混合</span></div>
                        <div class="team-card-desc">格局扫描→多竞对并行分析→战略定位→Battle Card</div>
                        <div class="team-card-roles"><span>格局扫描</span><span>竞对分析</span><span>策略输出</span></div>
                    </div>
                </div>
            </div>
            <div class="team-panel-note" data-i18n="teamPanelNote">专家团队自由协作 · 工作流按固定步骤执行 · AI员工面向具体岗位</div>
        </div>`;
        overlay.querySelectorAll('.team-card').forEach(function(card) {
            card.setAttribute('role', 'button');
            card.tabIndex = 0;
            card.addEventListener('keydown', function(event) {
                if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault();
                    card.click();
                }
            });
        });
        overlay.querySelectorAll('[data-i18n]').forEach(function(element) {
            if (typeof window.openKittyT === 'function') element.textContent = window.openKittyT(element.dataset.i18n, element.textContent);
        });
        document.body.appendChild(overlay);
    }
    if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(overlay);
    overlay.classList.add('open');
    setTimeout(function() {
        var icos = overlay.querySelectorAll('.ico[data-ico]');
        for (var i = 0; i < icos.length; i++) {
            var nm = icos[i].getAttribute('data-ico');
            if (window.I && window.I[nm]) icos[i].innerHTML = window.I[nm];
        }
    }, 10);
};

window.closeTeamPanel = function() {
    document.getElementById('teamOverlay').classList.remove('open');
};

window.useTeamTemplate = function(template) {
    var input = document.getElementById('messageInput');
    var teamNames = {
        research: '研究团队（研究员+分析师+撰稿人）',
        coding: '编码团队（架构师+程序员+审查员）',
        fullstack: '全栈团队（PM+后端+前端+测试）',
        content: '内容团队（策划+写手+编辑）',
        ppt: 'PPT工坊（内容策划→设计排版→视觉审核）',
        codebase: '代码审计（项目扫描→文件分析→架构审核）',
        contract: '合同审查（结构扫描→条款分析→合规审核）',
        competitor: '竞对情报（格局扫描+竞对分析×5→策略输出）'
    };
    var ph = {
        research: '请描述你的研究课题，例如：AI大模型最新进展',
        coding: '请描述你的开发需求，例如：实现一个REST API服务',
        fullstack: '请描述你的系统需求，例如：一个在线商城系统',
        content: '请描述你的内容主题，例如：量子计算科普系列',
        ppt: '请描述演示需求，例如：Q2季度业务复盘汇报，含营收/用户/产品线数据',
        codebase: '请描述分析目标，例如：分析 MACS 项目的架构和核心模块',
        contract: '请上传或粘贴合同内容，例如：一份 SaaS 服务订阅协议',
        competitor: '请描述分析目标，例如：中国AI大模型市场竞对格局'
    }[template] || '请描述你的任务';
    var cmd = '请调用 assemble_team 工具组建' + teamNames[template] + '，task=' + ph;
    if (input) {
        input.value = cmd;
        input.focus();
        // Select the placeholder text so user can just type to replace it
        var start = cmd.indexOf(ph);
        if (start >= 0) {
            input.setSelectionRange(start, start + ph.length);
        }
    }
    switchMode('single');
    document.getElementById('teamOverlay').classList.remove('open');
    // Don't auto-send — let user fill in the actual task first
};

// ==================== Cache Monitor ====================
var cacheChartInstance = null;
var tokenChartInstance = null;

function fmtNum(n) {
    n = Number(n || 0);
    if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M';
    if (n >= 1000) return (n / 1000).toFixed(1) + 'K';
    return String(n);
}

function hasCacheMetricData(snap, hist) {
    if (((snap && snap.total_cache_hit_tokens) || 0) + ((snap && snap.total_cache_miss_tokens) || 0) > 0) return true;
    return (hist || []).some(function(r) {
        return ((r.cache_hit_tokens || 0) + (r.cache_miss_tokens || 0)) > 0;
    });
}

async function loadMonitorData() {
    try {
        loadCacheConfig();
        var [snapRes, histRes] = await Promise.all([
            fetch('/api/monitor/cache'),
            fetch('/api/monitor/cache/history?n=50')
        ]);
        if (!snapRes.ok || !histRes.ok) throw new Error('HTTP error');
        var snap = await snapRes.json();
        var hist = await histRes.json();
        var hasCacheMetrics = hasCacheMetricData(snap, hist);
        var cacheNotice = document.getElementById('cacheMetricNotice');
        if (cacheNotice) {
            var hasCalls = (snap.total_calls || 0) > 0 || (hist && hist.length > 0);
            cacheNotice.style.display = (!hasCacheMetrics && hasCalls) ? 'block' : 'none';
            cacheNotice.textContent = '当前模型未返回缓存指标。请求仍然正常，输入/输出 Token 和延迟统计继续可用。';
        }

        // Stats cards
        document.getElementById('statHitRate').textContent = hasCacheMetrics ? ((snap.cache_hit_rate || 0).toFixed(1) + '%') : '未返回';
        document.getElementById('statHitTokens').textContent = hasCacheMetrics ? fmtNum(snap.total_cache_hit_tokens || 0) : '未返回';
        document.getElementById('statMissTokens').textContent = hasCacheMetrics ? fmtNum(snap.total_cache_miss_tokens || 0) : '未返回';
        document.getElementById('statAvgLatency').textContent = snap.avg_latency_ms ? snap.avg_latency_ms + 'ms' : '--';
        document.getElementById('statTotalCalls').textContent = snap.total_calls || '0';
        document.getElementById('statTotalPrompt').textContent = fmtNum(snap.total_prompt_tokens || 0);
        document.getElementById('statTotalCompletion').textContent = fmtNum(snap.total_completion_tokens || 0);
        document.getElementById('statTotalTokens').textContent = fmtNum((snap.total_prompt_tokens || 0) + (snap.total_completion_tokens || 0));

        // Chart
        if (hist && hist.length > 0) {
            var labels = hist.map(function(r) {
                var d = new Date(r.time);
                return d.getHours().toString().padStart(2,'0') + ':' + d.getMinutes().toString().padStart(2,'0') + ':' + d.getSeconds().toString().padStart(2,'0');
            });
            var rates = hist.map(function(r) {
                var total = r.cache_hit_tokens + r.cache_miss_tokens;
                return total > 0 ? parseFloat((r.cache_hit_tokens / total * 100).toFixed(1)) : 0;
            });
            var ctx = document.getElementById('cacheChart').getContext('2d');
            if (cacheChartInstance) cacheChartInstance.destroy();
            cacheChartInstance = new Chart(ctx, {
                type: 'line',
                data: {
                    labels: labels,
                    datasets: [{
                        label: 'Cache Hit %',
                        data: rates,
                        borderColor: '#6366f1',
                        backgroundColor: 'rgba(99,102,241,0.08)',
                        fill: true,
                        tension: 0.3,
                        pointRadius: 1.5,
                        borderWidth: 1.5
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: { legend: { display: false } },
                    scales: {
                        x: { ticks: { font: { size: 9 }, maxTicksLimit: 10, maxRotation: 0 } },
                        y: { min: 0, max: 100, ticks: { font: { size: 9 }, callback: function(v) { return v + '%'; } } }
                    },
                    interaction: { intersect: false, mode: 'index' }
                }
            });
        }

        // Token breakdown bar chart (cache hit/miss + completion)
        if (hist && hist.length > 0) {
            var tokenCtx = document.getElementById('tokenChart').getContext('2d');
            if (tokenChartInstance) tokenChartInstance.destroy();
            var barLabels = hist.map(function(r) {
                var d = new Date(r.time);
                return d.getHours().toString().padStart(2,'0') + ':' + d.getMinutes().toString().padStart(2,'0');
            });
            tokenChartInstance = new Chart(tokenCtx, {
                type: 'bar',
                data: {
                    labels: barLabels,
                    datasets: [
                        {
                            label: 'Cache Hit',
                            data: hist.map(function(r) { return r.cache_hit_tokens; }),
                            backgroundColor: '#22c55e',
                            borderColor: '#22c55e',
                            borderWidth: 0,
                            borderRadius: 0
                        },
                        {
                            label: 'Cache Miss',
                            data: hist.map(function(r) { return r.cache_miss_tokens; }),
                            backgroundColor: '#f59e0b',
                            borderColor: '#f59e0b',
                            borderWidth: 0,
                            borderRadius: 0
                        },
                        {
                            label: 'Completion',
                            data: hist.map(function(r) { return r.completion_tokens; }),
                            backgroundColor: '#6366f1',
                            borderColor: '#6366f1',
                            borderWidth: 0,
                            borderRadius: 2
                        }
                    ]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: { legend: { position: 'top', labels: { boxWidth: 12, font: { size: 10 }, padding: 8 } } },
                    scales: {
                        x: {
                            stacked: true,
                            ticks: { font: { size: 8 }, maxTicksLimit: 15, maxRotation: 0, autoSkip: true }
                        },
                        y: {
                            stacked: true,
                            ticks: { font: { size: 9 }, callback: function(v) { return fmtNum(v); } }
                        }
                    },
                    interaction: { intersect: false, mode: 'index' }
                }
            });
        }

        // Detail table
        var tbody = document.getElementById('cacheDetailBody');
        if (hist && hist.length > 0) {
            var rows = '';
            var recent = hist.slice(-10).reverse();
            recent.forEach(function(r) {
                var total = r.cache_hit_tokens + r.cache_miss_tokens;
                var rowHasCacheMetrics = total > 0;
                var rate = rowHasCacheMetrics ? (r.cache_hit_tokens / total * 100).toFixed(1) + '%' : '未返回';
                var t = new Date(r.time);
                var timeStr = t.getHours().toString().padStart(2,'0') + ':' + t.getMinutes().toString().padStart(2,'0') + ':' + t.getSeconds().toString().padStart(2,'0');
                var dlLink = '--';
                if (r.id || r.id === 0) {
                    dlLink = '<a href="/api/monitor/cache/prompt?id=' + r.id + '" download="prompt_' + r.id + '.json" style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6366f1;text-decoration:none;" title="下载该次调用的Prompt JSON">⬇</a>';
                }
                rows += '<tr>' +
                    '<td>' + timeStr + '</td>' +
                    '<td>' + (r.model || '--') + '</td>' +
                    '<td>' + (r.agent_name || '--') + '</td>' +
                    '<td>' + fmtNum(r.prompt_tokens) + '</td>' +
                    '<td>' + fmtNum(r.completion_tokens) + '</td>' +
                    '<td style="font-weight:600;">' + fmtNum(r.prompt_tokens + r.completion_tokens) + '</td>' +
                    '<td style="color:#22c55e;">' + (rowHasCacheMetrics ? fmtNum(r.cache_hit_tokens) : '--') + '</td>' +
                    '<td style="color:#f59e0b;">' + (rowHasCacheMetrics ? fmtNum(r.cache_miss_tokens) : '--') + '</td>' +
                    '<td style="font-weight:600;">' + rate + '</td>' +
                    '<td>' + (r.latency_ms || 0) + 'ms</td>' +
                    '<td>' + dlLink + '</td>' +
                    '</tr>';
            });
            tbody.innerHTML = rows;
        } else {
            tbody.innerHTML = '<tr><td colspan="11" style="text-align:center;color:#94a3b8;">暂无数据 — 发送对话后自动采集</td></tr>';
        }
    } catch (err) {
        console.error('loadMonitorData:', err);
        document.getElementById('cacheDetailBody').innerHTML = '<tr><td colspan="11" style="text-align:center;color:#ef4444;">加载失败: ' + err.message + '</td></tr>';
    }
}

async function loadCacheConfig() {
    try {
        var res = await fetch('/api/monitor/cache/config');
        if (!res.ok) throw new Error('HTTP ' + res.status);
        var data = await res.json();
        var sel = document.getElementById('semaphoreSelect');
        if (sel && data.semaphore_limit) {
            // Set dropdown to current value
            for (var i = 0; i < sel.options.length; i++) {
                if (parseInt(sel.options[i].value) === data.semaphore_limit) {
                    sel.selectedIndex = i;
                    break;
                }
            }
        }
        var info = document.getElementById('cacheAgentInfo');
        if (info && data.agents && data.agents.length > 0) {
            var names = data.agents.map(function(a) { return a.name + '(' + a.model + ')'; }).join(', ');
            info.textContent = '当前并发: ' + data.semaphore_limit + ' | Agent: ' + names;
        }
    } catch (e) { console.error('loadCacheConfig:', e); }
}

window.setSemaphore = function(mode) {
    var sel = document.getElementById('semaphoreSelect');
    var limit = parseInt(sel.value);
    if (mode === 'now') {
        fetch('/api/monitor/cache/config', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({semaphore_limit: limit})
        }).then(function(r) { return r.json(); }).then(function(d) {
            alert(d.message || '已更新');
            loadCacheConfig();
        }).catch(function(e) { alert('更新失败: ' + e.message); });
    } else {
        fetch('/api/config/reload', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({runtime: {semaphore_limit: limit}})
        }).then(function(r) { return r.json(); }).then(function(d) {
            alert('已保存，重启后并发数将变为 ' + limit);
        }).catch(function(e) { alert('保存失败: ' + e.message); });
    }
};

window.downloadSessionJSON = function(taskId) {
    var sessionId = taskId;
    var idx = taskId.lastIndexOf('-t');
    if (idx > 0) sessionId = taskId.substring(0, idx);
    var url = '/api/session/' + encodeURIComponent(sessionId);
    fetch(url).then(function(r) { return r.json(); }).then(function(data) {
        var blob = new Blob([JSON.stringify(data, null, 2)], {type: 'application/json'});
        var a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = sessionId + '.json';
        a.click();
        URL.revokeObjectURL(a.href);
    }).catch(function(e) { alert('下载失败: ' + e.message); });
};


window.openTrace = async function(taskId) {
    try {
        var res = await fetch('/api/trace/' + encodeURIComponent(taskId));
        var data = await res.json();
        var html = '<h3 style="margin-bottom:12px;">任务追踪: ' + taskId + '</h3>';
        if (!data.steps || data.steps.length === 0) {
            html += '<p style="color:#94a3b8;">暂无追踪数据。发送对话后自动采集。</p>';
        } else {
            html += '<table class="help-table" style="font-size:calc(12.1px * var(--macs-font-scale, 1));"><thead><tr><th>时间</th><th>类型</th><th>详情</th><th>Token</th><th>缓存命中</th><th>耗时</th></tr></thead><tbody>';
            for (var i = 0; i < data.steps.length; i++) {
                var s = data.steps[i];
                var icon = s.type === 'llm' ? '🤖' : '📋';
                var hitRate = s.tokens > 0 ? (s.cache_hit / s.tokens * 100).toFixed(0) + '%' : '-';
                html += '<tr><td>' + s.time + '</td><td>' + icon + ' ' + s.type + '</td><td>' + (s.detail || '') + '</td><td>' + (s.tokens || '-') + '</td><td>' + hitRate + '</td><td>' + (s.duration_ms || 0) + 'ms</td></tr>';
            }
            html += '</tbody></table>';
        }
        document.getElementById('traceBody').innerHTML = html;
        document.getElementById('traceModal').classList.add('active');
    } catch (e) {
        alert('获取追踪数据失败: ' + e.message);
    }
};

window.openTracePrompt = async function() {
    try {
        document.querySelectorAll('.dropdown-menu.show').forEach(function(d) { d.classList.remove('show'); });
        var res = await fetch('/api/tasks?limit=50');
        if (!res.ok) throw new Error('HTTP ' + res.status);
        var payload = await res.json();
        var tasks = Array.isArray(payload) ? payload : (payload && Array.isArray(payload.tasks) ? payload.tasks : []);
        var html = '<h3 style="margin-bottom:12px;">选择要追踪的任务</h3>';
        if (!tasks || tasks.length === 0) {
            html += '<p style="color:#94a3b8;">暂无任务记录。发送对话后自动生成。</p>';
        } else {
            tasks.sort(function(a, b) {
                return new Date(b.created_at || 0) - new Date(a.created_at || 0);
            });
            html += '<div style="max-height:50vh;overflow-y:auto;">';
            for (var i = 0; i < Math.min(tasks.length, 20); i++) {
                var t = tasks[i];
                html += '<div style="padding:8px 0;border-bottom:1px solid #f0f0f0;cursor:pointer;" onclick="document.getElementById(\'tracePromptModal\').classList.remove(\'active\');openTrace(' + jsCallArg(t.id) + ')">';
                html += '<b>' + escapeHtml((t.id || '').substring(0, 30)) + '</b> ';
                var pp = t.pipeline_progress;
                var progressText = pp ? (' | ' + escapeHtml(pp.pipeline_name || '') + ' ' + (pp.current_step || 0) + '/' + (pp.total_steps || 0)) : '';
                html += '<span style="color:#6b7280;font-size:calc(13.2px * var(--macs-font-scale, 1));">' + escapeHtml(t.status || '') + progressText + ' | ' + escapeHtml((t.message || '').substring(0, 40)) + '</span>';
                html += '</div>';
            }
            html += '</div>';
        }
        document.getElementById('tracePromptBody').innerHTML = html;
        document.getElementById('tracePromptModal').classList.add('active');
    } catch (e) {
        alert('获取任务列表失败: ' + e.message);
    }
};

var statusRefreshTimer = null;

function ensureStatusPanelStyles() {
    if (document.getElementById('statusPanelRuntimeStyles')) return;
    var style = document.createElement('style');
    style.id = 'statusPanelRuntimeStyles';
    style.textContent =
        '.status-grid{display:grid;grid-template-columns:1fr 1fr;gap:12px}' +
        '.status-card{background:#fff;border:1px solid #e5e7eb;border-radius:10px;overflow:hidden}' +
        '.status-card-header{background:#f9fafb;padding:10px 14px;font-weight:600;font-size:calc(15.4px * var(--macs-font-scale, 1));border-bottom:1px solid #e5e7eb}' +
        '.status-card-body{padding:14px}' +
        '@media(max-width:600px){.status-grid{grid-template-columns:1fr}.status-card[style*="grid-column"]{grid-column:auto!important}}';
    document.head.appendChild(style);
}

function ensureStatusModal() {
    ensureStatusPanelStyles();
    var modal = document.getElementById('statusModal');
    if (modal) return modal;

    var wrapper = document.createElement('div');
    wrapper.innerHTML =
        '<div id="statusModal" class="modal" style="display:none">' +
        '<div class="modal-content" style="max-width:800px;max-height:85vh;overflow-y:auto">' +
        '<div class="modal-header">' +
        '<h3>📊 系统状态</h3>' +
        '<button onclick="closeStatusPanel()" style="border:none;background:none;font-size:calc(22px * var(--macs-font-scale, 1));cursor:pointer">&times;</button>' +
        '</div>' +
        '<div class="modal-body" id="statusBody">' +
        '<div style="text-align:center;padding:40px;color:#9ca3af">正在获取系统状态...</div>' +
        '</div>' +
        '</div>' +
        '</div>';
    document.body.appendChild(wrapper.firstElementChild);
    return document.getElementById('statusModal');
}

window.openStatusPanel = async function() {
    document.querySelectorAll('.dropdown-menu.show').forEach(function(d) { d.classList.remove('show'); });
    var modal = ensureStatusModal();
    if (!modal) return;
    modal.style.display = 'flex';
    modal.classList.add('active');
    await refreshStatusPanel();
    if (statusRefreshTimer) clearInterval(statusRefreshTimer);
    statusRefreshTimer = setInterval(refreshStatusPanel, 30000);
};

window.closeStatusPanel = function() {
    var modal = document.getElementById('statusModal');
    if (modal) {
        modal.classList.remove('active');
        modal.style.display = 'none';
    }
    if (statusRefreshTimer) {
        clearInterval(statusRefreshTimer);
        statusRefreshTimer = null;
    }
};

async function refreshStatusPanel() {
    var body = document.getElementById('statusBody');
    if (!body) return;

    try {
        var resp = await fetch('/api/status');
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        var data = await resp.json();
        var mode = data.mode || 'default';
        var colors = {default:'#6b7280', acceptEdits:'#22c55e', bypassPermissions:'#ef4444', plan:'#3b82f6'};
        var labels = {default:'默认', acceptEdits:'自动编辑', bypassPermissions:'全部放行', plan:'计划模式'};
        body.innerHTML =
            '<div class="status-grid">' +
            '<div class="status-card">' +
            '<div class="status-card-header">权限模式</div>' +
            '<div class="status-card-body">' +
            '<span style="display:inline-block;padding:4px 12px;border-radius:20px;font-weight:700;font-size:calc(15.4px * var(--macs-font-scale, 1));background:' + (colors[mode] || '#6b7280') + ';color:#fff">' + escapeHtml(labels[mode] || mode) + '</span>' +
            (data.plan ? '<span style="display:inline-block;margin-left:8px;padding:3px 10px;border-radius:12px;background:#3b82f6;color:#fff;font-size:calc(13.2px * var(--macs-font-scale, 1))">PLAN MODE</span>' : '') +
            '</div></div>' +
            '<div class="status-card">' +
            '<div class="status-card-header">快速统计</div>' +
            '<div class="status-card-body">' +
            '<div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;font-size:calc(14.3px * var(--macs-font-scale, 1))">' +
            '<div><strong>Agents:</strong> ' + (data.agents || 0) + '</div>' +
            '<div><strong>Hooks:</strong> ' + (data.hooks || 0) + '</div>' +
            '<div><strong>Tools:</strong> ' + (data.tools || 0) + '</div>' +
            '<div><strong>Version:</strong> ' + escapeHtml(data.version || '?') + '</div>' +
            '<div><strong>Git:</strong> ' + escapeHtml(data.git_branch || '-') + '</div>' +
            '<div><strong>Cron:</strong> ' + (data.cron_tasks || 0) + '</div>' +
            '</div></div></div>' +
            '<div class="status-card" style="grid-column:span 2">' +
            '<div class="status-card-header">工具指标</div>' +
            '<div class="status-card-body" style="max-height:220px;overflow:auto;font-size:calc(13.2px * var(--macs-font-scale, 1));white-space:pre-wrap">' + escapeHtml(data.tool_metrics || '暂无数据') + '</div>' +
            '</div></div>';
    } catch (e) {
        body.innerHTML = '<div style="color:#ef4444;text-align:center;padding:20px">获取状态失败: ' + escapeHtml(e.message) + '</div>';
    }
}

// ==================== Marketplace ====================
function openMarketplace() {
    var panel = document.getElementById('marketplacePanel');
    if (panel) {
        panel.style.cssText = 'display:flex;justify-content:center;align-items:center;position:fixed;top:0;left:0;width:100%;height:100%;z-index:9999;';
        var dd = document.getElementById('toolMenu');
        if (dd) dd.classList.remove('show');
    }
}
window.openMarketplace = openMarketplace;
function closeMarketplace() {
    var panel = document.getElementById('marketplacePanel');
    if (panel) { panel.style.display = 'none'; }
    var detail = document.getElementById('marketDetail');
    if (detail) detail.style.display = 'none';
}
window.closeMarketplace = closeMarketplace;
function showMarketDetail(id) {
    var detail = document.getElementById('marketDetail');
    if (!detail) return;
    detail.style.display = 'block';
    var guides = {
        'nodejs': '<h4>🟢 Node.js 安装</h4><p>下载: <a href="https://nodejs.org" target="_blank">nodejs.org</a> → 选 LTS 版本 → 安装后重启终端。</p><code>node -v</code><p style="margin-top:8px;color:#64748b;">MACS 的 PPT 生成 (better-ppt) 依赖 Node.js 运行 pptxgenjs。</p>',
        'python': '<h4>🐍 Python 3 安装</h4><p>下载: <a href="https://python.org" target="_blank">python.org</a> → 安装时勾选 Add to PATH。</p><p>必需库: <code>pip install pandas matplotlib seaborn python-pptx pillow</code></p><p style="margin-top:8px;color:#64748b;">数据分析、图表生成、PPTX 创建、OCR 识别都依赖 Python。</p>',
        'git': '<h4>🔀 Git 安装</h4><p>下载: <a href="https://git-scm.com" target="_blank">git-scm.com</a></p><code>git --version</code><p style="margin-top:8px;color:#64748b;">项目周报统计代码变更量依赖 git log。</p>',
        'claude-code': '<h4>🟪 Claude Code 安装</h4><p>需要 Node.js 18+</p><code>npm install -g @anthropic-ai/claude-code</code><p>运行: <code>claude</code></p><p style="margin-top:8px;color:#64748b;">Anthropic 官方 CLI Agent。可在 MACS 中通过 persistent_shell 调用。</p>',
        'codex': '<h4>🟠 OpenAI Codex 安装</h4><p>需要 Node.js 18+</p><code>npm install -g @openai/codex</code><p>运行: <code>codex</code></p><p style="margin-top:8px;color:#64748b;">OpenAI 官方 CLI Agent。</p>',
        'gemini-cli': '<h4>🔵 Gemini CLI 安装</h4><p>需要 Node.js 18+</p><code>npm install -g @google/gemini-cli</code><p>运行: <code>gemini</code></p><p style="margin-top:8px;color:#64748b;">Google 官方 CLI Agent，有免费配额。</p>',
        'obsidian': '<h4>🟣 Obsidian 安装</h4><p>下载: <a href="https://obsidian.md/download" target="_blank">obsidian.md/download</a></p><p>安装后选一个文件夹作为 Vault。</p><p style="margin-top:8px;"><b>MACS 已集成</b>：对话中说"分析我的 vault"即自动调用 <code>vault_explore</code> 工具扫描。</p><p>技能文件: <code>skills/obsidian-sync/SKILL.md</code></p>',
        'notion': '<h4>📝 Notion 连接</h4><p>MACS 通过 web_fetch 工具访问 Notion 页面（需公开分享链接）。</p><p style="margin-top:8px;color:#64748b;">暂不支持 Notion API 直接集成，可通过导出 Markdown 后放入 storage/ 目录读取。</p>',
        'logseq': '<h4>📒 Logseq 连接</h4><p>Logseq 是开源大纲式笔记，数据以 Markdown 文件存储。MACS 的 vault_explore 工具可直接读取 Logseq 的 pages/ 目录。</p><p>路径示例: <code>vault_explore(action="stats", path="/path/to/logseq/pages")</code></p>',
        'skills-github': '<h4>🐙 GitHub Skills Hub</h4><p>从 GitHub 安装技能包:</p><code>curl -X POST http://localhost:8090/api/skill/install -H "Content-Type: application/json" -d "{\"url\":\"https://github.com/xxx/skill-name.git\"}"</code><p>或手动: <code>git clone https://github.com/xxx/skill-name.git skills/skill-name</code></p><p>重启 MACS 自动加载。技能需包含 SKILL.md 文件。</p><p style="margin-top:8px;color:#64748b;">推荐: obsidian-skills (33k stars)、Grimoire、open-second-brain</p>',
        'skills-npm': '<h4>📦 npm Skills Registry</h4><p>通过 npm 安装:</p><code>npm install -g skill-package-name</code><p>然后在 skills/ 目录创建 SKILL.md 引用该包。</p><p style="margin-top:8px;color:#64748b;">适用于基于 Node.js 的技能包。</p>',
        'ollama': '<h4>🦙 Ollama 安装</h4><p>下载: <a href="https://ollama.com/download" target="_blank">ollama.com/download</a></p><p>验证: <code>ollama --version</code></p><p style="margin-top:8px;">MACS 已集成 Ollama Provider (BaseURL: http://localhost:11434/v1)</p><p>拉模型: <code>ollama pull qwen3</code> 或 <code>ollama pull llama3.2-vision</code></p><p style="margin-top:8px;color:#64748b;">配合 HIPAA 工作流 + redact_pii 实现合规本地医疗处理。</p>',
        'medllama2': '<h4>🏥 MedLlama2</h4><p>临床医学大模型 (Llama2 + 医疗语料微调)</p><code>ollama pull medllama2</code><p style="margin-top:8px;">能力: 病历解析 · 症状分析 · 诊断建议 · 药物相互作用</p><p style="margin-top:8px;color:#64748b;">DE 配置 Ollama Provider → model_binding 选 medllama2 → 配合 redact_pii 脱敏。</p>',
        'biomistral': '<h4>🧬 BioMistral</h4><p>生物医学专用模型 (Mistral + PubMed 微调)</p><code>ollama pull biomistral</code><p style="margin-top:8px;">能力: 医学文献摘要 · 论文分析 · 临床试验解读 · 循证医学</p><p style="margin-top:8px;color:#64748b;">配置 Ollama Provider → 配合 RAG 知识库检索文献。</p>',
    };
    detail.innerHTML = guides[id] || '<p>暂无详情</p>';
    detail.scrollIntoView({behavior:'smooth'});
}

// Debug mode toggle
function isDebugMode() {
    return localStorage.getItem("debugMode") === "true";
}
window.toggleDebugMode = function() {
    var checkbox = document.getElementById("debugModeToggle");
    if (!checkbox) return;
    var checked = checkbox.checked;
    localStorage.setItem("debugMode", checked ? "true" : "false");
};
// Init debug checkbox on load
(function() {
    var cb = document.getElementById("debugModeToggle");
    if (cb) cb.checked = isDebugMode();
})();

function collectDisabledToolsFromSettings() {
    var configured = (currentConfig && currentConfig.tools_config && Array.isArray(currentConfig.tools_config.disabled_tools))
        ? currentConfig.tools_config.disabled_tools
        : [];
    var seen = Object.create(null);
    var disabled = [];
    configured.forEach(function(value) {
        var name = String(value || '').trim();
        var key = name.toLowerCase();
        if (!name || key === 'debug' || seen[key]) return;
        seen[key] = true;
        disabled.push(name);
    });

    var debugToolEnabled = document.getElementById('debugToolEnabled');
    if (!debugToolEnabled) {
        var debugWasDisabled = configured.some(function(value) { return String(value || '').trim().toLowerCase() === 'debug'; });
        if (debugWasDisabled) disabled.push('debug');
    } else if (!debugToolEnabled.checked) {
        disabled.push('debug');
    }
    return disabled;
}

function syncModbusSettingsState(clearWriteWhenDisabled) {
    var enabled = document.getElementById('modbusToolEnabled');
    var writeEnabled = document.getElementById('modbusWriteEnabled');
    var writeControl = document.getElementById('modbusWriteControl');
    var active = !!(enabled && enabled.checked);
    if (writeEnabled) {
        writeEnabled.disabled = !active;
        if (!active && clearWriteWhenDisabled !== false) writeEnabled.checked = false;
    }
    if (writeControl) writeControl.style.opacity = active ? '1' : '0.55';
}

function collectModbusConfigFromSettings() {
    var existing = settingsDeepClone(currentConfig?.tools_config?.modbus || {}) || {};
    var enabled = !!document.getElementById('modbusToolEnabled')?.checked;
    existing.enabled = enabled;
    existing.allow_write = enabled && !!document.getElementById('modbusWriteEnabled')?.checked;
    existing.timeout_ms = parseInt(existing.timeout_ms, 10) || 3000;
    if (!existing.targets || typeof existing.targets !== 'object') existing.targets = {};
    return existing;
}

// Strip debug info from progress messages when not in debug mode
window.filterDebugMsg = function(msg) {
    if (!msg) return msg;
    if (isDebugMode()) return msg;
    return msg.replace(/\s*\(\d+ tokens\)/g, "").replace(/\s*\(\d+ chars\)/g, "").replace(/\s*\(\d+ tokens, static cached\)/g, "").replace(/\(prompt: \d+ tokens, static cached\)/, "");
};

// Compact settings list + modal editors for models, agents, and channels.
function settingsText(key, fallback) {
    return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : (fallback || key);
}

function settingsTemplate(key, fallback, values) {
    var text = settingsText(key, fallback);
    Object.keys(values || {}).forEach(function(name) {
        text = text.split('{' + name + '}').join(String(values[name] == null ? '' : values[name]));
    });
    return text;
}

function settingsDeepClone(value) {
    if (value == null) return value;
    try { return JSON.parse(JSON.stringify(value)); } catch (e) { return value; }
}

function settingsHumanModelReason(reason) {
    reason = String(reason || '');
    if (reason.indexOf('model_name') >= 0) return settingsText('settingsModelNameRequired', '模型名称未填写');
    if (reason.indexOf('api_key') >= 0) return settingsText('settingsAPIKeyRequired', 'API Key 未配置或仍是占位值');
    return reason || settingsText('settingsIncomplete', '配置不完整');
}

function settingsBadge(label, tone) {
    return '<span class="settings-badge settings-badge-' + escapeAttr(tone || 'off') + '">' + escapeHtml(label || '') + '</span>';
}

function settingsChip(label) {
    return '<span class="settings-chip">' + escapeHtml(label || '-') + '</span>';
}

function settingsSecretSummary(value, notRequired) {
    if (notRequired) return settingsText('settingsAPIKeyLocal', 'API Key: 本地模型无需');
    return settingSuspiciousAPIKey(value)
        ? settingsText('settingsAPIKeyInvalid', 'API Key: 未配置/无效')
        : settingsText('settingsAPIKeyConfigured', 'API Key: 已填写');
}

function settingsField(label, controlHtml, hint, full) {
    return '<div class="settings-editor-field' + (full ? ' full' : '') + '">' +
        '<label>' + escapeHtml(label) + '</label>' +
        controlHtml +
        (hint ? '<div class="settings-editor-hint">' + escapeHtml(hint) + '</div>' : '') +
        '</div>';
}

function settingsEnsureEditorModal() {
    var modal = document.getElementById('settingsItemEditorModal');
    if (modal) return modal;
    modal = document.createElement('div');
    modal.id = 'settingsItemEditorModal';
    modal.className = 'modal settings-editor-modal';
    modal.innerHTML =
        '<div class="modal-content" style="max-width:760px;width:92vw;max-height:88vh;padding:0;overflow:hidden;">' +
        '<div class="modal-header" style="padding:14px 22px;">' +
        '<h3 id="settingsItemEditorTitle">' + escapeHtml(settingsText('settingsEdit', '编辑')) + '</h3>' +
        '<span class="close-modal" onclick="closeSettingsItemEditor()">&times;</span>' +
        '</div>' +
        '<div id="settingsItemEditorBody" class="settings-editor-body"></div>' +
        '<div id="settingsItemEditorFeedback" class="settings-editor-feedback" role="status" aria-live="polite"></div>' +
        '<div class="settings-editor-actions">' +
        '<button type="button" class="settings-action-btn" onclick="closeSettingsItemEditor()">' + escapeHtml(settingsText('settingsCancel', '取消')) + '</button>' +
		'<button type="button" id="settingsItemEditorSave" class="settings-action-btn primary">' + escapeHtml(settingsText('settingsApplyEdit', '保存并应用')) + '</button>' +
        '</div>' +
        '</div>';
    modal.addEventListener('click', function(e) {
        if (e.target === modal) closeSettingsItemEditor();
    });
    document.body.appendChild(modal);
    return modal;
}

function settingsOpenEditor(kind, title, bodyHtml, refValue) {
    var modal = settingsEnsureEditorModal();
    delete modal.dataset.composerCreateAgent;
    modal.setAttribute('data-kind', kind);
    modal.setAttribute('data-ref', refValue == null ? '' : String(refValue));
    document.getElementById('settingsItemEditorTitle').textContent = title;
    var body = document.getElementById('settingsItemEditorBody');
    body.innerHTML = bodyHtml;
    body.oninput = function() { settingsSetEditorDirty(true); settingsClearConnectionResult(); };
    body.onchange = function() { settingsSetEditorDirty(true); settingsClearConnectionResult(); };
    document.getElementById('settingsItemEditorSave').onclick = function() { saveConfig(); };
    settingsSetEditorDirty(false);
    document.getElementById('settingsItemEditorFeedback').textContent = '';
    modal.classList.add('active');
}

function closeSettingsItemEditor(force) {
    if (settingsSaving && force !== true) return false;
    var modal = document.getElementById('settingsItemEditorModal');
    if (!modal || !modal.classList.contains('active')) return true;
    if (force !== true && settingsEditorDirty && !confirm(settingsText('settingsEditorDiscardConfirm', '当前编辑内容尚未完成，确定放弃吗？'))) {
        return false;
    }
    modal.classList.remove('active');
    settingsSetEditorDirty(false);
    return true;
}

function settingsApplyActiveEditor() {
    var modal = document.getElementById('settingsItemEditorModal');
    if (!modal || !modal.classList.contains('active')) return true;
    return commitSettingsEditor(false);
}

function commitSettingsEditor(closeAfter) {
    var modal = document.getElementById('settingsItemEditorModal');
    if (!modal) return true;
    var kind = modal.getAttribute('data-kind') || '';
    var ref = modal.getAttribute('data-ref') || '';
    var ok = true;
    if (kind === 'agent') ok = commitAgentEditor(parseInt(ref, 10));
    else if (kind === 'model') ok = commitModelEditor(ref);
    else if (kind === 'channel') ok = commitChannelEditor(parseInt(ref, 10));
    if (!ok) return false;
    // A failed save must be retryable without adding the same item twice.
    if ((kind === 'agent' || kind === 'channel') && parseInt(ref, 10) < 0) modal.setAttribute('data-ref', String(currentConfig[kind === 'agent' ? 'agents' : 'channels'].length - 1));
    if (kind === 'model') modal.setAttribute('data-ref', document.querySelector('#settingsItemEditorBody .model-key').value.trim());
    settingsRefreshConfigHealth();
    settingsSetEditorDirty(false);
    if (closeAfter !== false) closeSettingsItemEditor(true);
    return true;
}

function settingsRefreshConfigHealth() {
    if (!currentConfig) return;
    var data = {
        model_config: currentConfig.model_config || {},
        agents: currentConfig.agents || [],
        channels: currentConfig.channels || []
    };
    var status = validateSettingsConfigData(data);
    status.available_models = settingAvailableModelNames(data.model_config);
	// Hooks are validated by the backend and are not duplicated by the compact
	// browser validator. Retain that server-only signal while localizing its
	// stable prefix.
	var serverErrors = currentConfig?.config_status?.errors || [];
	for (var i = 0; i < serverErrors.length; i++) {
		var serverError = String(serverErrors[i] || '');
		if (serverError.indexOf('Hooks 配置无效：') === 0 || serverError.indexOf('Hooks configuration is invalid:') === 0) {
			var reason = serverError.replace(/^Hooks 配置无效：|^Hooks configuration is invalid:\s*/, '');
			status.errors.push(settingsTemplate('configHealthHooksInvalid', 'Hooks 配置无效：{reason}', {reason: reason}));
			status.ok = false;
		}
	}
    renderConfigHealth(status);
}

function settingsSelectedModel(config) {
    config = config || {};
    if (config.default_model) return config.default_model;
    var preferred = preferredAssistantAgent(config);
    var names = settingAvailableModelNames(config.model_config || {});
    if (preferred?.model && names.indexOf(preferred.model) >= 0) return preferred.model;
    return names[0] || '';
}

function settingAgentStatus(agent) {
    var models = currentConfig?.model_config || {};
    var effectiveModel = agent?.model || settingsSelectedModel(currentConfig);
    if (!agent || !effectiveModel) {
        return {label: settingsText('settingsAgentNoModel', '未选模型'), tone: 'error', reason: settingsText('settingsAgentNoModelReason', 'Agent 还没有绑定模型')};
    }
    if (!models[effectiveModel]) {
        return {label: settingsText('settingsModelMissing', '模型不存在'), tone: 'error', reason: settingsText('settingsModelMissingReason', '绑定的模型配置不存在')};
    }
    var reason = settingModelUnavailableReason(models[effectiveModel]);
    if (reason) {
        return {label: settingsText('settingsModelUnavailable', '模型不可用'), tone: 'error', reason: settingsHumanModelReason(reason)};
    }
    return {label: settingsText('settingsAvailable', '可用'), tone: 'ok', reason: ''};
}

function settingsChooseDefaultAssistant(name) {
    currentConfig.default_agent = name;
    settingsSetDirty(true);
    renderAgentsList();
}

function settingsBindDefaultModel(name) {
    if (!currentConfig.agents) currentConfig.agents = [];
    var agent = preferredAssistantAgent(currentConfig);
    if (!agent) {
        agent = {name: 'Assistant', type: 'custom', model: '', system_message: 'You are a helpful assistant.', has_memory: true, memory_window: 50};
        currentConfig.agents.push(agent);
    }
    currentConfig.default_model = name;
    currentConfig.default_agent = agent.name;
}

function settingsSaveError(message) {
    var modal = document.getElementById('settingsItemEditorModal');
    var feedback = document.getElementById('settingsItemEditorFeedback');
    if (modal?.classList.contains('active') && feedback) feedback.textContent = message;
    else if (typeof showToast === 'function') showToast(message, 'error');
    else alert(message);
}

var settingsProviderPresets = {
    platform: {label: '平台模型', url: 'https://www.sicurrent.com/api/macs/v1'},
    openai: {label: 'OpenAI / ' + '兼容服务', url: 'https://api.openai.com/v1'},
    deepseek: {label: 'DeepSeek', url: 'https://api.deepseek.com'},
    minimax: {label: 'MiniMax', url: 'https://api.minimaxi.com/v1'},
    ollama: {label: 'Ollama (' + '本地模型' + ')', url: 'http://localhost:11434'}
};
var settingsConnectionSequence = 0;

function settingsProviderSelect(type) {
    var providers = Object.assign({}, settingsProviderPresets);
    if (type && !providers[type]) providers[type] = {label: type};
    return '<select class="model-type" onchange="settingsApplyProviderPreset(true)">' + Object.keys(providers).map(function(key) {
        return '<option value="' + escapeAttr(key) + '"' + (key === (type || 'openai') ? ' selected' : '') + '>' + escapeHtml(key === 'platform' ? settingsText('settingsPlatformModel', '使用平台模型') : providers[key].label) + '</option>';
    }).join('') + '</select>';
}

function settingsApplyProviderPreset(changed) {
    var body = document.getElementById('settingsItemEditorBody');
    var platform = body?.querySelector('.model-type')?.value === 'platform';
    var preset = settingsProviderPresets[body?.querySelector('.model-type')?.value];
    var address = body?.querySelector('.model-base-url');
    if (preset && address && (changed || !address.value)) address.value = preset.url;
    if (changed) {
        body.querySelector('.model-model-name').value = platform ? 'platform:default' : '';
        var platformKeyMode = body.querySelector('.model-platform-key-mode');
        if (platformKeyMode) platformKeyMode.checked = false;
        var key = body.querySelector('.model-api-key');
        key.value = '';
        settingsSetEditorDirty(true);
        settingsClearConnectionResult();
    }
    var hint = body?.querySelector('.model-source-hint');
    var useKey = platform && !!body?.querySelector('.model-platform-key-mode')?.checked;
    if (hint) hint.textContent = platform ? (useKey ? settingsText('settingsPlatformKeyBillingHint', '按访问密钥所属账号扣费；取消高级选项后，可直接使用本人登录账号。') : settingsText('settingsPlatformHint', '登录平台账号后直接使用，无需填写 API Key。成功调用按后台价格扣除本人账号余额。')) : settingsText('settingsCustomHint', '使用自己的服务地址和密钥，费用由服务商收取，不扣平台余额。');
    var keyField = body?.querySelector('.model-api-key')?.closest('.settings-editor-field');
    if (keyField) {
        keyField.hidden = platform && !useKey;
        if (platform && !useKey) body.querySelector('.model-api-key').value = '';
        keyField.querySelector('label').textContent = platform ? settingsText('settingsAccountKey', '账号访问密钥') : settingsText('settingsAPIKey', '接口密钥（API Key）');
        keyField.querySelector('.settings-editor-hint').textContent = platform ? settingsText('settingsAccountKeyHint', '在平台用户中心的积分钱包创建访问密钥。填写你自己的账号密钥，无需获取平台模型的服务商密钥。') : settingsText('settingsAPIKeyHint', '从 AI 服务商处获取。本地服务未要求认证时可留空；已保存的密钥保持原样即可。');
    }
    var keyMode = body?.querySelector('.model-platform-key-control');
    if (keyMode) keyMode.hidden = !platform;
    var catalog = body?.querySelector('#settingsLoadPlatformModels');
    if (catalog) catalog.hidden = !platform;
}

function settingsModelProviderType(value) {
    return value === 'platform' ? 'openai' : value;
}

async function loadSettingsPlatformModels() {
    var body = document.getElementById('settingsItemEditorBody');
    if (body?.querySelector('.model-type')?.value !== 'platform') return;
    var button = body.querySelector('#settingsLoadPlatformModels');
    var result = body.querySelector('#settingsConnectionResult');
    var modal = document.getElementById('settingsItemEditorModal');
    var controller = new AbortController();
    var timeout = setTimeout(function() { controller.abort(); }, 20000);
    var sequence = ++settingsConnectionSequence;
    button.disabled = true;
    result.textContent = settingsText('settingsLoadingModels', '正在读取平台模型…');
    try {
        var response = await fetch('/api/config/model-catalog', {method: 'POST', headers: {'Content-Type': 'application/json'}, signal: controller.signal, body: JSON.stringify({name: modal.getAttribute('data-ref') || '', model: {type: 'openai', base_url: body.querySelector('.model-base-url').value.trim(), api_key: body.querySelector('.model-api-key').value}})});
        var data = await response.json();
        if (!response.ok || !data.ok) throw new Error(data.code || 'connection_failed');
        if (sequence !== settingsConnectionSequence) return;
        var list = body.querySelector('#settingsPlatformModelOptions');
        list.replaceChildren();
        data.models.forEach(function(model) {
            var option = document.createElement('option');
            option.value = model.id;
            option.label = model.name + ' · ' + window.formatPlatformModelPrice(model);
            list.appendChild(option);
        });
        result.textContent = settingsText('settingsModelsLoaded', '列表已更新，可选择模型或保留平台默认。');
    } catch (error) {
        if (sequence === settingsConnectionSequence) result.textContent = settingsText('settingsCatalogFailed', '读取失败，请检查平台地址；后台模型接口需已上线。');
    } finally {
        clearTimeout(timeout);
        if (sequence === settingsConnectionSequence) button.disabled = false;
    }
}

function settingsClearConnectionResult() {
    settingsConnectionSequence++;
    var result = document.getElementById('settingsConnectionResult');
    if (result) result.textContent = '';
    var button = document.getElementById('settingsTestConnection');
    if (button) button.disabled = false;
    var catalogButton = document.getElementById('settingsLoadPlatformModels');
    if (catalogButton) catalogButton.disabled = false;
}

async function testSettingsModelConnection() {
    var body = document.getElementById('settingsItemEditorBody');
    var modal = document.getElementById('settingsItemEditorModal');
    if (!body || modal?.getAttribute('data-kind') !== 'model') return;
    var result = document.getElementById('settingsConnectionResult');
    var button = document.getElementById('settingsTestConnection');
    var model = {
        type: settingsModelProviderType(body.querySelector('.model-type').value),
        api_key: body.querySelector('.model-api-key').value,
        model_name: body.querySelector('.model-model-name').value.trim(),
        base_url: body.querySelector('.model-base-url').value.trim()
    };
    var reason = settingModelUnavailableReason(model);
    if (reason || !model.base_url) {
        result.textContent = reason ? settingsHumanModelReason(reason) : settingsText('settingsURLRequired', '请填写服务地址');
        return;
    }
    var sequence = ++settingsConnectionSequence;
    button.disabled = true;
    result.textContent = settingsText('settingsTesting', '正在连接…');
    var controller = new AbortController();
    var timeout = setTimeout(function() { controller.abort(); }, 25000);
    try {
        var response = await fetch('/api/config/model-test', {
            method: 'POST', headers: {'Content-Type': 'application/json'}, signal: controller.signal,
            body: JSON.stringify({name: modal.getAttribute('data-ref') || '', model: model})
        });
        var data = await response.json().catch(function() { return {}; });
        if (!response.ok || !data.ok) throw new Error(data.code || 'connection_failed');
        if (sequence === settingsConnectionSequence) result.textContent = settingsText('settingsTestSuccess', '连接成功，可以保存并开始对话');
    } catch (err) {
        var errors = {
            login_required: ['settingsPlatformLoginRequired', '请先在左下角登录平台账号，再使用平台模型。'],
            platform_address_mismatch: ['settingsPlatformAddressMismatch', '模型地址与登录平台不一致，请重新添加当前平台的模型。'],
            unauthorized: ['settingsTestAuthError', '密钥未通过验证，请检查密钥和账号权限。'],
            model_unavailable: ['settingsTestModelError', '服务无法使用这个模型，请检查模型 ID 和账号权限。'],
            rate_limited: ['settingsTestLimitError', '服务请求受限，请检查额度或稍后重试。'],
            saved_key_address_changed: ['settingsTestKeyAddressError', '服务地址已改变，请重新填写该服务的密钥后测试。'],
            invalid_config: ['settingsTestInvalidError', '请检查模型名称和服务地址。'],
            unsupported_provider: ['settingsTestUnsupported', '此服务暂不支持连接测试，请保存后通过对话验证。'],
            local_only: ['settingsTestLocalOnly', '请在运行 macs 的本机打开设置进行连接测试。'],
            invalid_response: ['settingsTestResponseError', '地址可以访问，但没有返回有效的 AI 回答，请检查接口地址。']
        };
        var copy = errors[err.message] || ['settingsTestNetworkError', '连接失败，请检查服务地址和网络后重试。'];
        if (sequence === settingsConnectionSequence) result.textContent = settingsText(copy[0], copy[1]);
    } finally {
        clearTimeout(timeout);
        if (sequence === settingsConnectionSequence) button.disabled = false;
    }
}

function renderSettingsCapabilities() {
    var container = document.getElementById('settingsOfficeCapabilities');
    if (!container) return;
    var disabled = currentConfig?.tools_config?.disabled_tools || [];
    var globallyEnabled = currentConfig?.tools_config?.enabled !== false;
    var choices = [
        ['web_fetch', 'settingsReadWeb', '阅读网页', 'settingsReadWebHint', '读取你提供的网页链接，用于整理资料和总结内容。'],
        ['extract', 'settingsProcessFiles', '处理办公文档', 'settingsProcessFilesHint', '读取文档、提取表格，按模板填写 Word 等文件。'],
        ['computer', 'settingsUseApps', '操作网页与应用', 'settingsUseAppsHint', '在网页或桌面应用中按步骤完成任务。']
    ];
    container.innerHTML = choices.map(function(choice) {
        var enabled = !disabled.some(function(name) { return String(name).toLowerCase() === choice[0]; });
        return '<div class="settings-capability-card"><div><strong>' + escapeHtml(settingsText(choice[1], choice[2])) + '</strong><p>' + escapeHtml(settingsText(choice[3], choice[4])) + '</p>' + settingsBadge(settingsText(globallyEnabled && enabled ? 'settingsEnabled' : 'settingsNotEnabled', globallyEnabled && enabled ? '已启用' : '未启用'), globallyEnabled && enabled ? 'ok' : 'off') + '</div><label class="switch"><input type="checkbox" aria-label="' + escapeAttr(settingsText(choice[1], choice[2])) + '" ' + (enabled && globallyEnabled ? 'checked' : '') + (globallyEnabled ? '' : ' disabled') + ' onchange="settingsToggleCapability(\'' + choice[0] + '\',this.checked)"><span class="slider"></span></label></div>';
    }).join('') + (globallyEnabled ? '' : '<p class="config-hint">' + escapeHtml(settingsText('settingsToolsGloballyOff', '当前配置已关闭全部工具；请联系维护人员开启。')) + '</p>');
}

function settingsToggleCapability(name, enabled) {
    if (!currentConfig.tools_config) currentConfig.tools_config = {};
    var disabled = (currentConfig.tools_config.disabled_tools || []).filter(function(value) { return String(value).toLowerCase() !== name; });
    if (!enabled) disabled.push(name);
    currentConfig.tools_config.disabled_tools = disabled;
    settingsSetDirty(true);
    renderSettingsCapabilities();
}

function renderSettingsAppChoices() {
    var container = document.getElementById('settingsAppChoices');
    if (!container) return;
    container.classList.add('settings-app-picker');
    var apps = [['feishu', '飞书'], ['wework', '企业微信'], ['dingtalk', '钉钉'], ['qq', 'QQ'], ['weixin', '微信 iLink']];
    container.innerHTML = apps.map(function(app) {
        var configured = (currentConfig.channels || []).some(function(channel) { return settingNormalizeChannelType(channel.type) === app[0]; });
        var guideLabel = settingsText('settingsChannelGuide', '接入指南');
        return '<div class="settings-app-tile"><button type="button" class="settings-app-choice" onclick="settingsConnectApp(\'' + app[0] + '\')"><strong>' + escapeHtml(app[1]) + '</strong><span>' + escapeHtml(settingsText(configured ? 'settingsManageApp' : 'settingsConnectApp', configured ? '管理连接' : '连接应用')) + '</span></button>' +
            '<button type="button" class="settings-channel-guide-btn" onclick="settingsShowChannelGuide(\'' + app[0] + '\')" title="' + escapeAttr(settingsText('settingsChannelGuideHint', '先看准备事项和操作步骤')) + '" aria-label="' + escapeAttr(app[1] + ' ' + guideLabel) + '">' + escapeHtml(guideLabel) + '</button></div>';
    }).join('');
}

function settingsShowChannelGuide(type) {
    var normalized = settingNormalizeChannelType(type);
    var supported = ['feishu', 'qq', 'dingtalk', 'wework', 'weixin', 'email', 'custom'];
    if (supported.indexOf(normalized) < 0) normalized = 'custom';
    if (typeof window.openChannelGuide === 'function') window.openChannelGuide(normalized);
}

function settingsShowConfiguredChannelGuide(index) {
    var channel = (currentConfig?.channels || [])[index];
    settingsShowChannelGuide(channel && channel.type);
}

function settingsConnectApp(type) {
    var index = (currentConfig.channels || []).findIndex(function(channel) { return settingNormalizeChannelType(channel.type) === type; });
    if (index >= 0) openChannelEditor(index);
    else openChannelEditor(-1, {name: settingDefaultChannelName(type), type: type, enabled: false, config: settingDefaultChannelConfig(type)});
}

function renderAgentsList() {
    var container = document.getElementById('agentsList');
    if (!container) return;
    var agentsList = currentConfig?.agents || [];
    if (agentsList.length === 0) {
        container.innerHTML = '<div style="text-align:center;color:#adb5bd;padding:20px;">' + escapeHtml(settingsText('settingsNoAgents', '暂无 Agent 配置')) + '</div>';
        return;
    }
    var preferredName = currentConfig?.default_agent || '';
    var entries = prioritizedAgentEntries(agentsList, preferredName);
    var html = settingsField(settingsText('settingsDefaultAssistant', '默认使用的助手'), '<select id="settingsDefaultAssistant" onchange="settingsChooseDefaultAssistant(this.value)">' + entries.map(function(entry) { return '<option value="' + escapeAttr(entry.agent.name) + '"' + (entry.agent.name === (preferredName || entries[0].agent.name) ? ' selected' : '') + '>' + escapeHtml(entry.agent.name) + '</option>'; }).join('') + '</select>', '', true) + '<div class="settings-summary-list">';
    for (var i = 0; i < entries.length; i++) {
        var sourceIndex = entries[i].index;
        var agent = entries[i].agent;
        var status = settingAgentStatus(agent);
        var isDefault = preferredName && String(agent.name || '').trim().toLowerCase() === String(preferredName).trim().toLowerCase();
        html += '<div class="settings-summary-card">' +
            '<div class="settings-card-main">' +
            '<div class="settings-card-title-row"><span class="settings-card-title">' + escapeHtml(agent.name || settingsText('settingsUnnamedAgent', '未命名 Agent')) + '</span>' + (isDefault ? settingsBadge(settingsText('settingsDefaultAgent', '默认'), 'off') : '') + settingsBadge(status.label, status.tone) + '</div>' +
            '<div class="settings-card-subtitle">' + escapeHtml(status.reason || (agent.system_message ? String(agent.system_message).slice(0, 90) : settingsText('settingsNoPrompt', '无系统提示词摘要'))) + '</div>' +
            '<div class="settings-card-meta">' +
            settingsChip(settingsText('settingsModel', '模型') + ': ' + (agent.model || settingsText('settingsInheritModel', '跟随当前选择的模型'))) +
            settingsChip(settingsText('settingsMemory', '记忆') + ': ' + (agent.has_memory === false ? settingsText('settingsOff', '关闭') : settingsText('settingsOn', '开启'))) +
            '</div></div>' +
            '<div class="settings-card-actions">' +
            '<button type="button" class="settings-action-btn" onclick="openAgentEditor(' + sourceIndex + ')">' + escapeHtml(settingsText('settingsEdit', '编辑')) + '</button>' +
            '<button type="button" class="settings-action-btn danger" onclick="removeAgent(' + sourceIndex + ')">' + escapeHtml(settingsText('settingsDelete', '删除')) + '</button>' +
            '</div></div>';
    }
    html += '</div>';
    container.innerHTML = html;
}

function openAgentEditor(index, draftAgent) {
    if (!currentConfig.agents) currentConfig.agents = [];
    var isNew = index == null || index < 0;
    var agent = isNew ? draftAgent : currentConfig.agents[index];
    if (!agent) return;
    var body = '<div class="settings-editor-grid">' +
        settingsField(settingsText('settingsName', '名称'), '<input type="text" class="agent-name" value="' + escapeHtml(agent.name || '') + '">', '', false) +
        settingsField(settingsText('settingsModel', '模型'), buildAgentModelSelect(agent, index), settingsText('settingsAgentModelHint', '默认跟随输入框旁选择的模型；单独选择后，这个 Agent 始终使用自己的模型。'), true) +
        settingsField(settingsText('settingsSystemMessage', '系统消息'), '<textarea class="agent-system-message" rows="6">' + escapeHtml(agent.system_message || '') + '</textarea>', '', true) +
        settingsField(settingsText('settingsEnableMemory', '记住对话'), '<label class="settings-check"><input type="checkbox" class="agent-has-memory" ' + (agent.has_memory === false ? '' : 'checked') + '> ' + escapeHtml(settingsText('settingsMemoryHelp', '保留对话记忆，方便继续上次的工作')) + '</label>', '', true) +
        '</div><details class="settings-details"><summary>' + escapeHtml(settingsText('settingsAssistantAdvanced', '更多回答选项')) + '</summary><div class="settings-details-body settings-editor-grid">' +
        settingsField(settingsText('settingsType', '类型'), '<input type="text" class="agent-type" value="' + escapeHtml(agent.type || 'custom') + '">', '', false) +
        settingsField(settingsText('settingsTemperature', '温度'), '<input type="number" step="0.1" min="0" max="2" class="agent-temperature" value="' + escapeHtml(String(agent.temperature == null ? 0.7 : agent.temperature)) + '">', '', false) +
        settingsField(settingsText('settingsMaxOutput', '最大输出 Token 数'), '<input type="number" class="agent-max-tokens" value="' + escapeHtml(String(agent.max_tokens || 4096)) + '">', '', false) +
        settingsField(settingsText('settingsMemoryWindow', '记忆窗口'), '<input type="number" class="agent-memory-window" value="' + escapeHtml(String(agent.memory_window || 50)) + '">', '', false) +
        '</div></details>';
    settingsOpenEditor('agent', isNew ? settingsText('settingsAddAgent', '添加 Agent') : settingsText('settingsEditAgent', '编辑 Agent'), body, isNew ? -1 : index);
}

function commitAgentEditor(index) {
    if (!currentConfig.agents) currentConfig.agents = [];
    var isNew = index == null || index < 0;
    if (!isNew && !currentConfig.agents[index]) return false;
    var body = document.getElementById('settingsItemEditorBody');
    var name = (body.querySelector('.agent-name')?.value || '').trim();
    if (!name) { alert(settingsText('settingsAgentNameRequired', 'Agent 名称不能为空')); return false; }
    var duplicate = currentConfig.agents.some(function(agent, agentIndex) {
        return (isNew || agentIndex !== index) && String(agent.name || '').trim().toLowerCase() === name.toLowerCase();
    });
    if (duplicate) { alert(settingsText('settingsAgentNameExists', 'Agent 名称已存在：') + name); return false; }
    var existing = isNew ? {} : currentConfig.agents[index];
    var previousName = String(existing.name || '').trim();
    // Preserve advanced/runtime-only fields that are not shown by the compact
    // editor (tool policies, skill directories, permission mode, iteration
    // limits, multimodal flags, and employee bindings).
    var nextAgent = Object.assign({}, existing, {
        name: name,
        type: (body.querySelector('.agent-type')?.value || 'custom').trim() || 'custom',
        model: (body.querySelector('.agent-model')?.value || '').trim(),
        system_message: body.querySelector('.agent-system-message')?.value || '',
        temperature: parseFloat(body.querySelector('.agent-temperature')?.value || '0.7'),
        max_tokens: parseInt(body.querySelector('.agent-max-tokens')?.value || '4096', 10) || 4096,
        has_memory: !!body.querySelector('.agent-has-memory')?.checked,
        memory_window: parseInt(body.querySelector('.agent-memory-window')?.value || '50', 10) || 50
    });
    if (isNew) currentConfig.agents.push(nextAgent);
    else currentConfig.agents[index] = nextAgent;
    if (previousName && String(currentConfig.default_agent || '').trim().toLowerCase() === previousName.toLowerCase()) {
        currentConfig.default_agent = name;
    }
    if (!currentConfig.default_agent) currentConfig.default_agent = name;
    renderAgentsList();
    settingsSetDirty(true);
    return true;
}

function addAgent() {
    if (!currentConfig.agents) currentConfig.agents = [];
    var name = 'new_agent';
    var suffix = 1;
    while (currentConfig.agents.some(function(agent) { return String(agent.name || '').trim().toLowerCase() === name.toLowerCase(); })) {
        suffix++;
        name = 'new_agent_' + suffix;
    }
    openAgentEditor(-1, {
        name: name,
        type: 'custom',
        model: '',
        system_message: 'You are a helpful assistant.',
        temperature: 0.7,
        max_tokens: 4096,
        has_memory: true,
        memory_window: 50
    });
}

function removeAgent(index) {
    if (!currentConfig.agents) return;
    if (confirm(settingsText('settingsDeleteAgentConfirm', '确定删除这个 Agent 吗？'))) {
        var removed = currentConfig.agents.splice(index, 1)[0] || {};
        if (String(currentConfig.default_agent || '').trim().toLowerCase() === String(removed.name || '').trim().toLowerCase()) {
            var nextDefault = preferredAssistantAgent({agents: currentConfig.agents, default_agent: ''});
            currentConfig.default_agent = nextDefault ? nextDefault.name : '';
        }
        renderAgentsList();
        settingsRefreshConfigHealth();
        settingsSetDirty(true);
    }
}

function settingModelStatus(model) {
    var reason = settingModelUnavailableReason(model);
    if (reason) return {label: settingsText('settingsIncomplete', '填写未完成'), tone: 'error', reason: settingsHumanModelReason(reason)};
    return {label: settingsText('settingsFilled', '已填写'), tone: 'ok', reason: ''};
}

function renderModelsList() {
    var container = document.getElementById('modelsList');
    if (!container) return;
    var models = currentConfig?.model_config || {};
    var modelNames = sortModelNamesAssistantFirst(Object.keys(models), currentConfig || {});
    if (modelNames.length === 0) {
        container.innerHTML = '<div style="text-align:center;color:#adb5bd;padding:20px;">' + escapeHtml(settingsText('settingsNoModels', '暂无模型配置')) + '</div>';
        return;
    }
    var html = '<div class="settings-summary-list">';
    for (var i = 0; i < modelNames.length; i++) {
        var name = modelNames[i];
        var model = models[name] || {};
        var status = settingModelStatus(model);
        var needsKey = settingModelRequiresAPIKey(model);
        html += '<div class="settings-summary-card">' +
            '<div class="settings-card-main">' +
            '<div class="settings-card-title-row"><span class="settings-card-title">' + escapeHtml(name) + '</span>' + settingsBadge(status.label, status.tone) + '</div>' +
            '<div class="settings-card-subtitle">' + escapeHtml(status.reason || (model.model_name || settingsText('settingsModelNameMissingText', '未填写模型名称'))) + '</div>' +
            '<div class="settings-card-meta">' +
            settingsChip(settingsText('settingsProvider', '服务商') + ': ' + (/\/api\/macs\/v1\/?$/.test(model.base_url || '') ? settingsText('settingsPlatformModel', '使用平台模型') : (model.type || '-'))) +
            settingsChip(settingsText('settingsModel', '模型') + ': ' + (model.model_name || '-')) +
            settingsChip(/\/api\/macs\/v1\/?$/.test(model.base_url || '') && !model.api_key && !model.api_key_env ? settingsText('settingsPlatformAccountBilling', '本人登录账号 · 余额扣费') : settingsSecretSummary(model.api_key, !needsKey)) +
            '</div></div>' +
            '<div class="settings-card-actions">' +
            '<button type="button" class="settings-action-btn" onclick="openModelEditor(' + jsCallArg(name) + ')">' + escapeHtml(settingsText('settingsEdit', '编辑')) + '</button>' +
            '<button type="button" class="settings-action-btn danger" onclick="removeModel(' + jsCallArg(name) + ')">' + escapeHtml(settingsText('settingsDelete', '删除')) + '</button>' +
            '</div></div>';
    }
    html += '</div>';
    container.innerHTML = html;
}

function openModelEditor(name, draftModel, draftName) {
    if (!currentConfig.model_config) currentConfig.model_config = {};
    var isNew = !name;
    var model = isNew ? draftModel : currentConfig.model_config[name];
    if (!model) return;
    var identifier = isNew ? (draftName || 'new_model') : name;
    var selected = settingsSelectedModel(currentConfig);
    var bind = !selected || !currentConfig.model_config[selected] || !!settingModelUnavailableReason(currentConfig.model_config[selected]);
    var body = '<div class="settings-editor-grid">' +
        settingsField(settingsText('settingsProvider', '服务商'), settingsProviderSelect(/\/api\/macs\/v1\/?$/.test(model.base_url || '') ? 'platform' : model.type), settingsText('settingsProviderHint', '选择你拥有账号的服务；也可以连接本地模型。'), true) +
        '<p class="config-hint model-source-hint" style="grid-column:1/-1;margin:0;"></p>' +
        settingsField(settingsText('settingsAPIKey', '接口密钥（API Key）'), '<input type="password" class="model-api-key" autocomplete="new-password" value="' + escapeAttr(model.api_key || '') + '">', settingsText('settingsAPIKeyHint', '从 AI 服务商处获取。本地服务未要求认证时可留空；已保存的密钥保持原样即可。'), true) +
        settingsField(settingsText('settingsModelName', '使用的模型'), '<input type="text" class="model-model-name" list="settingsPlatformModelOptions" value="' + escapeAttr(model.model_name || '') + '" placeholder="' + escapeAttr(settingsText('settingsModelExample', '填写服务商提供的模型 ID')) + '"><datalist id="settingsPlatformModelOptions"></datalist>', settingsText('settingsModelIDHint', '需要与你的账号可用模型一致。'), true) +
        settingsField(settingsText('settingsBaseURL', '服务地址'), '<input type="url" class="model-base-url" value="' + escapeAttr(model.base_url || '') + '">', settingsText('settingsServiceURLHint', '已填写默认地址；使用代理或其他兼容服务时，请填它提供的接口地址。'), true) +
        settingsField(settingsText('settingsBindDefault', '设为当前模型'), '<label class="settings-check"><input type="checkbox" class="model-bind-default" ' + (bind ? 'checked' : '') + '> ' + escapeHtml(settingsText('settingsBindDefaultHint', '保存后，未单独指定模型的 Agent 跟随这个模型')) + '</label>', '', true) +
        '</div><div class="settings-connection-test"><button type="button" class="settings-action-btn" id="settingsLoadPlatformModels" onclick="loadSettingsPlatformModels()" hidden>' + escapeHtml(settingsText('settingsLoadModels', '读取平台模型')) + '</button><button type="button" class="settings-action-btn" id="settingsTestConnection" onclick="testSettingsModelConnection()">' + escapeHtml(settingsText('settingsTestConnection', '测试连接')) + '</button><span id="settingsConnectionResult" role="status" aria-live="polite"></span><p class="config-hint">' + escapeHtml(settingsText('settingsTestHint', '会发送一条简短测试消息，服务商可能收取少量费用。测试不会保存设置。')) + '</p></div>' +
        '<details class="settings-details"><summary>' + escapeHtml(settingsText('settingsModelAdvanced', '更多模型选项')) + '</summary><div class="settings-details-body settings-editor-grid">' +
        '<label class="settings-check model-platform-key-control" hidden style="grid-column:1/-1"><input type="checkbox" class="model-platform-key-mode" ' + (model.api_key || model.api_key_env ? 'checked' : '') + ' onchange="settingsApplyProviderPreset(false);settingsSetEditorDirty(true);settingsClearConnectionResult()"> ' + escapeHtml(settingsText('settingsPlatformAccessKeyMode', '使用账号访问密钥（供脚本或第三方接入）')) + '</label>' +
        settingsField(settingsText('settingsModelIdentifier', '服务名称'), '<input type="text" class="model-key" value="' + escapeAttr(identifier) + '">', settingsText('settingsModelIdentifierHint', '用来区分多个服务，改名会同步更新助手的引用。'), true) +
        settingsField(settingsText('settingsTemperature', '回答变化程度'), '<input type="number" step="0.1" min="0" max="2" class="model-temperature" value="' + escapeAttr(String(model.temperature == null ? 0.7 : model.temperature)) + '">', '', false) +
        settingsField(settingsText('settingsMaxOutput', '单次回答长度上限（Token）'), '<input type="number" min="1" class="model-max-tokens" value="' + escapeAttr(String(model.max_tokens || 4096)) + '">', '', false) +
        '</div></details>';
    settingsOpenEditor('model', isNew ? settingsText('settingsAddModel', '连接 AI 服务') : settingsText('settingsEditModel', '编辑 AI 服务'), body, isNew ? '' : name);
    settingsConnectionSequence++;
    settingsApplyProviderPreset(false);
}


function commitModelEditor(oldName) {
    if (!currentConfig.model_config) currentConfig.model_config = {};
    var body = document.getElementById('settingsItemEditorBody');
    var newName = (body.querySelector('.model-key')?.value || '').trim();
    if (!newName) { alert(settingsText('settingsModelIdentifierRequired', '模型标识不能为空')); return false; }
    if (newName !== oldName && currentConfig.model_config[newName]) {
        alert(settingsText('settingsModelIdentifierExists', '模型标识已存在：') + newName);
        return false;
    }
    var existing = oldName ? (currentConfig.model_config[oldName] || {}) : {};
    var originalName = String(existing.__previous_name || oldName || '').trim();
    var model = Object.assign({}, existing, {
        type: settingsModelProviderType((body.querySelector('.model-type')?.value || '').trim()),
        api_key: body.querySelector('.model-api-key')?.value || '',
        base_url: (body.querySelector('.model-base-url')?.value || '').trim(),
        model_name: (body.querySelector('.model-model-name')?.value || '').trim(),
        temperature: parseFloat(body.querySelector('.model-temperature')?.value || '0.7'),
        max_tokens: parseInt(body.querySelector('.model-max-tokens')?.value || '4096', 10) || 4096
    });
    if (body.querySelector('.model-type')?.value === 'platform' && !body.querySelector('.model-platform-key-mode')?.checked) {
        model.api_key = '';
        delete model.api_key_env;
    }
    if (originalName && newName !== originalName) model.__previous_name = originalName;
    else delete model.__previous_name;
    if (oldName && newName !== oldName) {
        delete currentConfig.model_config[oldName];
        if (currentConfig.default_model === oldName) currentConfig.default_model = newName;
        (currentConfig.agents || []).forEach(function(agent) {
            if (agent.model === oldName) agent.model = newName;
        });
    }
    currentConfig.model_config[newName] = model;
    if (!settingModelUnavailableReason(model) && body.querySelector('.model-bind-default')?.checked) settingsBindDefaultModel(newName);
    renderModelsList();
    renderAgentsList();
    settingsSetDirty(true);
    return true;
}

function addModel() {
    if (!currentConfig.model_config) currentConfig.model_config = {};
    var name = 'new_model';
    var suffix = 1;
    while (currentConfig.model_config[name]) {
        suffix++;
        name = 'new_model_' + suffix;
    }
    openModelEditor('', {
        type: 'openai',
        api_key: '',
        base_url: '',
        model_name: '',
        temperature: 0.7,
        max_tokens: 4096
    }, name);
}

async function addPlatformModel(selectedID) {
    var name = 'platform_model';
    var suffix = 1;
    while (currentConfig?.model_config?.[name]) name = 'platform_model_' + (++suffix);
    var platformAddress = settingsProviderPresets.platform.url;
    try {
        var response = await fetch('/api/account/config', {cache: 'no-store'});
        var data = await response.json();
        if (response.ok && /^https?:\/\//.test(data.platform_model_base_url || '')) platformAddress = data.platform_model_base_url;
    } catch (error) { /* The configured platform will still validate the account endpoint at call time. */ }
    openModelEditor('', {type: 'openai', api_key: '', base_url: platformAddress, model_name: selectedID || 'platform:default', temperature: 0.7, max_tokens: 4096}, name);
    if (selectedID) {
        var bindDefault = document.querySelector('#settingsItemEditorBody .model-bind-default');
        if (bindDefault) bindDefault.checked = true;
    }
}

function removeModel(name) {
    if (!currentConfig.model_config || !currentConfig.model_config[name]) return;
    if (confirm(settingsText('settingsDeleteModelConfirm', '确定删除模型配置 {name} 吗？').replace('{name}', name))) {
        var wasSelected = settingsSelectedModel(currentConfig) === name;
        delete currentConfig.model_config[name];
        if (wasSelected) { currentConfig.default_model = ''; currentConfig.default_model = settingsSelectedModel(currentConfig); }
        renderModelsList();
        renderAgentsList();
        settingsRefreshConfigHealth();
        settingsSetDirty(true);
    }
}

function settingChannelSchemaLegacy(type) {
    type = String(type || '').toLowerCase();
    if (type === 'openclaw-weixin') type = 'weixin';
    return CHANNEL_SCHEMAS[type] || null;
}

function settingChannelRequiredFieldsLegacy(type) {
    type = String(type || '').toLowerCase();
    if (type === 'openclaw-weixin') type = 'weixin';
    var map = {
        feishu: ['app_id', 'app_secret'],
        qq: ['app_id', 'app_secret'],
        dingtalk: ['client_id', 'client_secret'],
        wework: ['bot_id', 'secret'],
        weixin: ['account_id', 'token'],
        email: ['smtp_host', 'smtp_port', 'imap_host', 'imap_port', 'username', 'password']
    };
    return map[type] || [];
}

function settingChannelFieldLabel(type, key) {
    var schema = settingChannelSchema(type);
    if (schema && schema.fields) {
        for (var i = 0; i < schema.fields.length; i++) {
            if (schema.fields[i].key === key) return schema.fields[i].label || key;
        }
    }
    return key;
}

function settingChannelInvalidValue(key, value) {
    if (value == null) return true;
    var text = String(value).trim();
    if (!text) return true;
    var lower = text.toLowerCase();
    var suspicious = ['your_', 'your-', 'placeholder', 'change_me', 'changeme', 'todo', 'undefined', 'null', 'xxx'];
    for (var i = 0; i < suspicious.length; i++) {
        if (lower.indexOf(suspicious[i]) >= 0) return true;
    }
    return false;
}

function settingChannelConfiguredCount(channel) {
    var config = channel.config || {};
    var keys = Object.keys(config).filter(function(key) {
        var value = config[key];
        if (Array.isArray(value)) return value.length > 0;
        if (typeof value === 'boolean') return value;
        return String(value == null ? '' : value).trim() !== '';
    });
    return keys.length;
}

function settingChannelStatus(channel) {
    channel = channel || {};
    var type = String(channel.type || '').toLowerCase();
    var schema = settingChannelSchema(type);
    if (!channel.enabled) {
        return {label: settingsText('settingsChannelDisabled', '未启用'), tone: 'off', reason: settingsText('settingsChannelDisabledReason', '启用后才会监听该渠道')};
    }
    if (!type) {
        return {label: settingsText('settingsChannelTypeMissing', '未选类型'), tone: 'warn', reason: settingsText('settingsChannelTypeMissingReason', '请选择渠道类型或填写自定义 JSON')};
    }
    var required = settingChannelRequiredFields(type);
    var missing = [];
    for (var i = 0; i < required.length; i++) {
        var key = required[i];
        if (settingChannelInvalidValue(key, (channel.config || {})[key])) {
            missing.push(settingChannelFieldLabel(type, key));
        }
    }
    if (missing.length) {
        return {label: settingsText('settingsChannelInvalid', '配置无效'), tone: 'error', reason: settingsText('settingsChannelMissingPrefix', '缺少或占位：') + missing.join(', ')};
    }
    if (!schema && settingChannelConfiguredCount(channel) === 0) {
        return {label: settingsText('settingsChannelEmpty', '配置为空'), tone: 'warn', reason: settingsText('settingsChannelEmptyReason', '自定义渠道没有填写配置')};
    }
    return {label: settingsText('settingsChannelConfigured', '已配置'), tone: 'ok', reason: settingsText('settingsChannelVerifyHint', '已填写必需项，请按指南验证消息收发')};
}

function settingChannelTypeOptionsLegacy(selected) {
    selected = String(selected || '');
    var known = [
        ['', '自定义 JSON'],
        ['feishu', '飞书 (feishu)'],
        ['qq', 'QQ (qq)'],
        ['dingtalk', '钉钉 (dingtalk)'],
        ['wework', '企业微信 (wework)'],
        ['weixin', '微信 iLink (weixin)'],
        ['email', '邮箱 (email)']
    ];
    var seen = {};
    var html = '';
    for (var i = 0; i < known.length; i++) {
        seen[known[i][0]] = true;
        html += '<option value="' + escapeAttr(known[i][0]) + '"' + (selected === known[i][0] ? ' selected' : '') + '>' + escapeHtml(known[i][1]) + '</option>';
    }
    if (selected && !seen[selected]) {
        html += '<option value="' + escapeAttr(selected) + '" selected>' + escapeHtml(selected + ' (custom)') + '</option>';
    }
    return html;
}

function renderChannelsList() {
    var container = document.getElementById('channelsList');
    if (!container) return;
    var channelList = currentConfig?.channels || [];
    if (channelList.length === 0) {
        container.innerHTML = '<div style="text-align:center;color:#adb5bd;padding:20px;">' + escapeHtml(settingsText('settingsNoChannels', '暂无渠道配置')) + '</div>';
        return;
    }
    var html = '<div class="settings-summary-list">';
    for (var i = 0; i < channelList.length; i++) {
        var channel = channelList[i] || {};
        var status = settingChannelStatus(channel);
        var schema = settingChannelSchema(channel.type);
        var typeTitle = schema ? schema.title : (channel.type || settingsText('settingsCustom', '自定义'));
        var isWeixin = String(channel.type || '').toLowerCase() === 'weixin' || String(channel.type || '').toLowerCase() === 'openclaw-weixin';
        html += '<div class="settings-summary-card">' +
            '<div class="settings-card-main">' +
            '<div class="settings-card-title-row"><span class="settings-card-title">' + escapeHtml(channel.name || settingsText('settingsUnnamedChannel', '未命名渠道')) + '</span>' + settingsBadge(status.label, status.tone) + '</div>' +
            '<div class="settings-card-subtitle">' + escapeHtml(status.reason || settingsText('settingsConfiguredCollapsed', '配置项已收起，点击编辑查看细节')) + '</div>' +
            '<div class="settings-card-meta">' +
            settingsChip(settingsText('settingsType', '类型') + ': ' + typeTitle) +
            settingsChip(settingsText('settingsEnabled', '启用') + ': ' + (channel.enabled ? settingsText('settingsYes', '是') : settingsText('settingsNo', '否'))) +
            settingsChip(settingsText('settingsConfiguredItems', '已填配置') + ': ' + settingChannelConfiguredCount(channel)) +
            '</div></div>' +
            '<div class="settings-card-actions">' +
            (isWeixin ? '<button type="button" class="settings-action-btn success" onclick="openWeixinQrLogin(' + i + ')">' + escapeHtml(settingsText('settingsScanConnect', '扫码连接')) + '</button>' : '') +
            '<button type="button" class="settings-action-btn settings-channel-guide-btn" onclick="settingsShowConfiguredChannelGuide(' + i + ')" title="' + escapeAttr(settingsText('settingsChannelGuideHint', '先看准备事项和操作步骤')) + '">' + escapeHtml(settingsText('settingsChannelGuide', '接入指南')) + '</button>' +
            '<button type="button" class="settings-action-btn" onclick="openChannelEditor(' + i + ')">' + escapeHtml(settingsText('settingsEdit', '编辑')) + '</button>' +
            '<button type="button" class="settings-action-btn danger" onclick="removeChannel(' + i + ')">' + escapeHtml(settingsText('settingsDelete', '删除')) + '</button>' +
            '</div></div>';
    }
    html += '</div>';
    container.innerHTML = html;
}

function settingChannelEditorFieldsHTMLLegacy(type, config, index) {
    type = String(type || '');
    config = config || {};
    var schema = settingChannelSchema(type);
    if (!schema) {
        return '<div class="settings-editor-grid">' +
            settingsField('自定义类型', '<input type="text" id="channelEditCustomType" value="' + escapeHtml(type) + '" placeholder="例如 telegram">', '', true) +
            settingsField('配置 JSON', '<textarea class="channel-config" rows="9">' + escapeHtml(JSON.stringify(config || {}, null, 2)) + '</textarea>', '自定义渠道会原样保存 JSON。', true) +
            '</div>';
    }
    var html = '';
    if (type === 'weixin') {
        html += '<div style="margin-bottom:12px;border:1px solid #bbf7d0;background:#f0fdf4;border-radius:8px;padding:12px;">' +
            '<button type="button" class="settings-action-btn success" onclick="startWeixinQrLogin(' + index + ')">扫码登录微信</button>' +
            '<span id="weixinQrStatus_' + index + '" style="margin-left:10px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;"></span>' +
            '<div id="weixinQrBox_' + index + '" style="margin-top:10px;"></div>' +
            '</div>';
    }
    if (schema.helpText) {
        html += '<div style="background:#eff6ff;border:1px solid #bfdbfe;border-radius:8px;padding:10px 12px;margin-bottom:12px;color:#1d4ed8;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">' + escapeHtml(schema.helpText) + '</div>';
    }
    html += '<div class="settings-editor-grid">';
    for (var i = 0; i < schema.fields.length; i++) {
        var field = schema.fields[i];
        var value = config[field.key] != null ? config[field.key] : '';
        var control = '';
        if (field.type === 'checkbox') {
            control = '<label style="display:flex;align-items:center;gap:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#334155;"><input type="checkbox" class="settings-channel-field channel-field-' + escapeAttr(field.key) + '" data-field-key="' + escapeAttr(field.key) + '" ' + (value ? 'checked' : '') + '> 开启</label>';
        } else {
            control = '<input type="' + escapeAttr(field.type || 'text') + '" class="settings-channel-field channel-field-' + escapeAttr(field.key) + '" data-field-key="' + escapeAttr(field.key) + '" value="' + escapeHtml(String(value)) + '" placeholder="' + escapeHtml(field.placeholder || '') + '">';
        }
        html += settingsField(field.label || field.key, control, field.help || '', field.key === 'allowed_users' || field.key === 'base_url' || field.key === 'bot_agent');
    }
    html += '</div>';
    return html;
}

function refreshChannelEditorFieldsLegacy() {
    var modal = document.getElementById('settingsItemEditorModal');
    if (!modal) return;
    var index = parseInt(modal.getAttribute('data-ref') || '-1', 10);
    var type = document.getElementById('channelEditType')?.value || '';
    var channel = currentConfig?.channels?.[index] || {};
    var config = String(channel.type || '') === String(type || '') ? (channel.config || {}) : {};
    var fields = document.getElementById('channelEditorFields');
    if (fields) fields.innerHTML = settingChannelEditorFieldsHTML(type, config, index);
}

function openChannelEditorLegacy(index) {
    if (!currentConfig.channels) currentConfig.channels = [];
    var channel = currentConfig.channels[index];
    if (!channel) return;
    var type = String(channel.type || '');
    var body = '<div class="settings-editor-grid">' +
        settingsField('名称', '<input type="text" class="channel-name" data-index="' + index + '" value="' + escapeHtml(channel.name || '') + '">', '', false) +
        settingsField('类型', '<select id="channelEditType" class="channel-type" data-index="' + index + '" onchange="refreshChannelEditorFields()">' + settingChannelTypeOptions(type) + '</select>', '', false) +
        settingsField('启用', '<label style="display:flex;align-items:center;gap:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#334155;"><input type="checkbox" class="channel-enabled" data-index="' + index + '" ' + (channel.enabled ? 'checked' : '') + '> enabled</label>', '', true) +
        '</div>' +
        '<div id="channelEditorFields">' + settingChannelEditorFieldsHTML(type, channel.config || {}, index) + '</div>';
    settingsOpenEditor('channel', '编辑渠道配置', body, index);
}

function commitChannelEditorLegacy(index) {
    if (!currentConfig.channels || !currentConfig.channels[index]) return false;
    var body = document.getElementById('settingsItemEditorBody');
    var name = (body.querySelector('.channel-name')?.value || '').trim();
    if (!name) { alert('渠道名称不能为空'); return false; }
    var type = body.querySelector('.channel-type')?.value || '';
    var schema = settingChannelSchema(type);
    var config = {};
    if (schema) {
        var inputs = body.querySelectorAll('.settings-channel-field');
        for (var i = 0; i < inputs.length; i++) {
            var input = inputs[i];
            var key = input.getAttribute('data-field-key');
            if (!key) continue;
            if (input.type === 'checkbox') {
                config[key] = !!input.checked;
            } else {
                var value = input.value.trim();
                if (value) config[key] = (key === 'smtp_port' || key === 'imap_port') ? (parseInt(value, 10) || value) : value;
            }
        }
    } else {
        var customType = (body.querySelector('#channelEditCustomType')?.value || '').trim();
        if (customType) type = customType;
        var jsonText = body.querySelector('.channel-config')?.value || '{}';
        try {
            config = JSON.parse(jsonText || '{}');
        } catch (e) {
            alert('渠道 JSON 解析失败: ' + e.message);
            return false;
        }
    }
    currentConfig.channels[index] = {
        name: name,
        type: type,
        enabled: !!body.querySelector('.channel-enabled')?.checked,
        config: config
    };
    renderChannelsList();
    return true;
}

function addChannelEditorLegacy() {
    openChannelEditor(-1);
}

function removeChannel(index) {
    if (!currentConfig.channels) return;
    if (confirm('确定删除这个渠道吗？')) {
        currentConfig.channels.splice(index, 1);
        renderChannelsList();
        settingsRefreshConfigHealth();
        settingsSetDirty(true);
    }
}

function saveChannelsConfig() {
    if (!settingsApplyActiveEditor()) return;
    saveConfig();
}

function onChannelTypeChange(index) {
    openChannelEditor(index);
}

async function startWeixinQrLoginLegacyA(index) {
    if (!settingsApplyActiveEditor()) return;
    openChannelEditor(index);
    var channel = currentConfig?.channels?.[index] || {};
    var channelName = channel.name || 'weixin';
    var statusEl = document.getElementById('weixinQrStatus_' + index);
    var boxEl = document.getElementById('weixinQrBox_' + index);
    if (statusEl) statusEl.textContent = '正在生成二维码...';
    if (boxEl) boxEl.innerHTML = '';
    try {
        var response = await fetch('/api/weixin/login/start', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({channel: channelName, force: true})
        });
        var data = await response.json();
        if (!response.ok || !data.ok || !data.qr_data_url) {
            throw new Error((data && (data.error || data.message)) || '二维码生成失败');
        }
        if (statusEl) statusEl.textContent = '请用手机微信扫码确认';
        if (boxEl) {
            var qr = String(data.qr_data_url || '');
            var qrHtml = '<div style="display:flex;align-items:flex-start;gap:12px;flex-wrap:wrap;">' +
                '<div>' +
                '<img id="weixinQrImage_' + index + '" src="' + escapeHtml(qr) + '" alt="Weixin QR" style="width:180px;height:180px;border:1px solid #e5e7eb;border-radius:8px;background:white;object-fit:contain;">' +
                '<div id="weixinQrImageError_' + index + '" style="display:none;width:180px;min-height:120px;border:1px dashed #fca5a5;border-radius:8px;padding:12px;color:#b91c1c;background:#fff5f5;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">二维码图片加载失败。请重新点击扫码连接，或查看后台 /api/weixin/login/start 返回。</div>' +
                '</div>' +
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#475569;line-height:1.7;max-width:360px;">扫码后本机会保存 iLink Bot token 到渠道配置。建议保留 allowed_users，只允许扫码用户触发 AI 员工。</div>' +
                '</div>';
            boxEl.innerHTML = qrHtml;
            var img = document.getElementById('weixinQrImage_' + index);
            var errBox = document.getElementById('weixinQrImageError_' + index);
            if (img && errBox) {
                img.onerror = function() {
                    img.style.display = 'none';
                    errBox.style.display = 'block';
                };
            }
        }
        pollWeixinQrLogin(data.session_key, channelName, index);
    } catch (err) {
        if (statusEl) statusEl.textContent = '扫码启动失败: ' + err.message;
        if (boxEl) boxEl.innerHTML = '';
    }
}

async function startWeixinQrLoginLegacyB(index) {
    index = parseInt(index, 10);
    if (isNaN(index)) index = -1;
    if (!currentConfig.channels) currentConfig.channels = [];

    var modal = document.getElementById('settingsItemEditorModal');
    var activeRef = modal ? parseInt(modal.getAttribute('data-ref') || '-1', 10) : -2;
    var activeKind = modal ? (modal.getAttribute('data-kind') || '') : '';
    var isSameChannelEditor = !!(modal && modal.classList.contains('active') && activeKind === 'channel' && activeRef === index);
    if (!isSameChannelEditor) {
        if (!settingsApplyActiveEditor()) return;
        if (index >= 0) {
            openChannelEditor(index);
        } else {
            openChannelEditor(-1, {
                name: 'weixin',
                type: 'weixin',
                enabled: true,
                config: settingDefaultChannelConfig('weixin')
            });
        }
    } else {
        var typeEl = document.getElementById('channelEditType');
        if (typeEl && settingNormalizeChannelType(typeEl.value || '') !== 'weixin') {
            typeEl.value = 'weixin';
            refreshChannelEditorFields();
        }
    }

    var body = document.getElementById('settingsItemEditorBody');
    var nameEl = body ? body.querySelector('.channel-name') : null;
    var channel = index >= 0 ? (currentConfig.channels[index] || {}) : {};
    var channelName = ((nameEl && nameEl.value) || channel.name || 'weixin').trim() || 'weixin';
    if (nameEl && !nameEl.value.trim()) nameEl.value = channelName;

    var statusEl = document.getElementById('weixinQrStatus_' + index);
    var boxEl = document.getElementById('weixinQrBox_' + index);
    if (!statusEl || !boxEl) {
        refreshChannelEditorFields();
        statusEl = document.getElementById('weixinQrStatus_' + index);
        boxEl = document.getElementById('weixinQrBox_' + index);
    }
    if (statusEl) statusEl.textContent = '正在生成二维码...';
    if (boxEl) {
        boxEl.innerHTML = '<div style="width:180px;height:180px;border:1px dashed #cbd5e1;border-radius:8px;display:flex;align-items:center;justify-content:center;color:#64748b;font-size:calc(13.2px * var(--macs-font-scale, 1));background:#f8fafc;">二维码生成中</div>';
    }

    try {
        var response = await fetch('/api/weixin/login/start', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({channel: channelName, force: true})
        });
        var data = await response.json().catch(function() { return {}; });
        if (!response.ok || !data.ok || !data.qr_data_url) {
            throw new Error((data && (data.error || data.message)) || '二维码生成失败');
        }
        if (statusEl) statusEl.textContent = '请用手机微信扫码确认';
        if (boxEl) {
            var qr = String(data.qr_data_url || '');
            var qrPrefix = qr.slice(0, 80);
            var qrHtml = '<div style="display:flex;align-items:flex-start;gap:12px;flex-wrap:wrap;">' +
                '<div>' +
                '<img id="weixinQrImage_' + index + '" src="' + escapeAttr(qr) + '" alt="Weixin QR" style="width:180px;height:180px;border:1px solid #e5e7eb;border-radius:8px;background:white;object-fit:contain;">' +
                '<div id="weixinQrImageError_' + index + '" style="display:none;width:220px;min-height:120px;border:1px dashed #fca5a5;border-radius:8px;padding:12px;color:#b91c1c;background:#fff5f5;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">二维码图片加载失败。返回长度: ' + qr.length + '<br>前缀: ' + escapeHtml(qrPrefix) + '</div>' +
                '</div>' +
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#475569;line-height:1.7;max-width:360px;">扫码后本机会保存 iLink Bot token 到渠道配置。建议保留 allowed_users，只允许扫码用户触发 AI 员工。</div>' +
                '</div>';
            boxEl.innerHTML = qrHtml;
            var img = document.getElementById('weixinQrImage_' + index);
            var errBox = document.getElementById('weixinQrImageError_' + index);
            if (img && errBox) {
                img.onerror = function() {
                    img.style.display = 'none';
                    errBox.style.display = 'block';
                };
            }
        }
        pollWeixinQrLogin(data.session_key, channelName, index);
    } catch (err) {
        if (statusEl) statusEl.textContent = '扫码启动失败: ' + err.message;
        if (boxEl) {
            boxEl.innerHTML = '<div style="border:1px dashed #fca5a5;border-radius:8px;padding:12px;color:#b91c1c;background:#fff5f5;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">' + escapeHtml(err.message || '二维码生成失败') + '</div>';
        }
    }
}

function openWeixinQrLoginLegacyA(index) {
    startWeixinQrLogin(index);
}

function addWeixinChannelAndLoginLegacyA() {
    if (!currentConfig.channels) currentConfig.channels = [];
    var idx = findWeixinChannelIndex();
    if (idx >= 0) {
        startWeixinQrLogin(idx);
        return;
    }
    startWeixinQrLogin(-1);
}

function openWeixinQrLoginLegacyB(index) {
    openChannelEditor(index);
    setTimeout(function() { startWeixinQrLogin(index); }, 50);
}

function addWeixinChannelAndLoginLegacyB() {
    if (!currentConfig.channels) currentConfig.channels = [];
    var idx = findWeixinChannelIndex();
    if (idx < 0) {
        openChannelEditor(-1, {
            name: 'weixin',
            type: 'weixin',
            enabled: true,
            config: settingDefaultChannelConfig('weixin')
        });
        return;
    }
    openWeixinQrLogin(idx);
}

function collectConfigData() {
    var modelConfig = settingsDeepClone(currentConfig?.model_config || {}) || {};
    var agentsConfig = settingsDeepClone(currentConfig?.agents || []) || [];
    var channelsConfig = settingsDeepClone(currentConfig?.channels || []) || [];
    var preferredAgent = preferredAssistantAgent({
        agents: agentsConfig,
        default_agent: currentConfig?.default_agent || ''
    });
    return {
		config_revision: currentConfig?.config_revision || '',
        team_name: document.getElementById('teamName')?.value || '',
        default_agent: currentConfig?.default_agent || (preferredAgent ? preferredAgent.name : ''),
        default_model: settingsSelectedModel(currentConfig),
        workspace: document.getElementById('workspace')?.value || '',
        headless: document.getElementById('headless')?.checked || false,
        memory_config: {
            enabled: document.getElementById('memoryEnabled')?.checked ?? (currentConfig?.memory_config?.enabled !== false)
        },
        output: {
            format: document.getElementById('outputFormat')?.value || 'json',
            dir: document.getElementById('outputDir')?.value || 'storage'
        },
        runtime: {
            semaphore_limit: parseInt(document.getElementById('semaphoreLimit')?.value, 10) || 4
        },
		gateway: {
            enabled: document.getElementById('gatewayEnabled')?.checked || false,
            listen_addr: document.getElementById('gatewayListenAddr')?.value || ':8080',
            max_tasks: parseInt(document.getElementById('gatewayMaxTasks')?.value, 10) || 10,
            task_timeout: parseInt(document.getElementById('gatewayTaskTimeout')?.value, 10) || 300,
            enable_webui: document.getElementById('gatewayEnableWebUI')?.checked || false,
            webui_path: document.getElementById('gatewayWebUIPath')?.value || './webui'
		},
		tools_config: {
			enabled: !(currentConfig?.tools_config?.enabled === false),
			disabled_tools: collectDisabledToolsFromSettings(),
			private_network_protection: document.getElementById('privateNetworkProtection')?.checked || false,
			modbus: collectModbusConfigFromSettings()
		},
		email_config: {
            enabled: document.getElementById('emailEnabled')?.checked || false,
            smtp_host: document.getElementById('smtpHost')?.value || '',
            smtp_port: parseInt(document.getElementById('smtpPort')?.value, 10) || 465,
            imap_host: document.getElementById('imapHost')?.value || '',
            imap_port: parseInt(document.getElementById('imapPort')?.value, 10) || 993,
            username: document.getElementById('emailUsername')?.value || '',
            password: document.getElementById('emailPassword')?.value || '',
            from_address: document.getElementById('fromAddress')?.value || '',
            use_tls: document.getElementById('useTLS')?.checked || false
        },
        agents: agentsConfig,
        model_config: modelConfig,
        channels: channelsConfig
    };
}

async function saveConfig() {
    if (settingsSaving) return false;
    if (!document.getElementById('configForm')) return false;
    if (!settingsApplyActiveEditor()) return false;

    var configData = collectConfigData();
    var editorModal = document.getElementById('settingsItemEditorModal');
    var createdAgentName = editorModal?.dataset.composerCreateAgent === 'true'
        ? document.querySelector('#settingsItemEditorBody .agent-name')?.value.trim() : '';
    var validation = validateSettingsConfigData(configData);
    if (!validation.ok) {
        settingsSaveError(settingsText('settingsCannotSave', '设置尚未完成：') + '\n' + validation.errors.join('\n'));
        if (validation.errors.some(function(message) {
            return message.indexOf('模型') >= 0 || message.indexOf('Agent') >= 0 || message.toLowerCase().indexOf('model') >= 0;
        })) {
            switchConfigTab('agents');
        }
        return false;
    }
    if (validation.warnings.length > 0 && !confirm('配置存在提醒：\n\n' + validation.warnings.join('\n') + '\n\n仍然保存吗？')) {
        return false;
    }

    settingsSetSaving(true);
    try {
        var response = await fetch('/api/config/sections', {
            method: 'PUT',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(configData)
        });
        var responseText = await response.text();
        var result = {};
        try { result = responseText ? JSON.parse(responseText) : {}; } catch (parseError) {}
        if (!response.ok || result.status !== 'success') {
            throw new Error(result.message || responseText || ('HTTP ' + response.status));
        }

        if (result.config_status) renderConfigHealth(result.config_status);
        await loadConfigToForm();
        if (typeof loadAgents === 'function') await loadAgents();
        if (typeof window.refreshComposerModels === 'function') await window.refreshComposerModels();
        if (createdAgentName) {
            var agentSelect = document.getElementById('agentSelect');
            if (agentSelect && Array.from(agentSelect.options).some(function (option) { return option.value === createdAgentName && !option.dataset.action; })) {
                agentSelect.value = createdAgentName;
                agentSelect.dispatchEvent(new Event('change'));
            }
        }
        settingsSetDirty(false);
        closeSettingsItemEditor(true);
        if (typeof showToast === 'function') showToast(settingsText('settingsSaved', '已保存并生效'), 'success');
        if (typeof addLog === 'function') addLog('info', result.message || '配置已保存并生效');
        return true;
    } catch (err) {
        settingsSaveError(settingsText('settingsSaveFailed', '保存失败，修改内容已保留：') + err.message);
        if (typeof addLog === 'function') addLog('error', '保存配置失败: ' + err.message);
        settingsSetDirty(true);
        return false;
    } finally {
        settingsSetSaving(false);
    }
}

// Channel editor corrections: keep fields aligned with openkitty-config.yaml
// and only add a new channel after the modal is confirmed.
CHANNEL_SCHEMAS = {
    feishu: {
        title: '飞书',
        helpText: 'openkitty-config.yaml: type: feishu, config.app_id/app_secret',
        fields: [
            {key: 'app_id', label: 'App ID', placeholder: 'cli_xxx', type: 'text'},
            {key: 'app_secret', label: 'App Secret', placeholder: '应用 App Secret', type: 'password'}
        ]
    },
    qq: {
        title: 'QQ',
        helpText: 'openkitty-config.yaml: type: qq, config.app_id/app_secret',
        fields: [
            {key: 'app_id', label: 'App ID', placeholder: '1903702067', type: 'text'},
            {key: 'app_secret', label: 'App Secret', placeholder: 'QQ Bot App Secret', type: 'password'}
        ]
    },
    dingtalk: {
        title: '钉钉',
        helpText: '请从钉钉开发者后台获取 Client ID、Client Secret 和 Robot Code，三项均需填写。查看接入指南了解准备和验证步骤。',
        fields: [
            {key: 'client_id', label: 'Client ID', placeholder: 'ding...', type: 'text'},
            {key: 'client_secret', label: 'Client Secret', placeholder: 'DingTalk Client Secret', type: 'password'},
            {key: 'robot_code', label: 'Robot Code', placeholder: '机器人 Robot Code', type: 'text', help: '请填写开发者后台的真实 Robot Code，不能留空。'}
        ]
    },
    wework: {
        title: '企业微信',
        helpText: 'openkitty-config.yaml: type: wework, config.bot_id/secret',
        fields: [
            {key: 'bot_id', label: 'Bot ID', placeholder: '企业微信机器人 Bot ID', type: 'text'},
            {key: 'secret', label: 'Secret', placeholder: '企业微信机器人 Secret', type: 'password'}
        ]
    },
    email: {
        title: '邮箱',
        helpText: 'openkitty-config.yaml: type: email。SMTP/IMAP/账号/授权码都需要填。',
        fields: [
            {key: 'smtp_host', label: 'SMTP 服务器', placeholder: 'smtp.qq.com', type: 'text'},
            {key: 'smtp_port', label: 'SMTP 端口', placeholder: '465', type: 'text'},
            {key: 'imap_host', label: 'IMAP 服务器', placeholder: 'imap.qq.com', type: 'text'},
            {key: 'imap_port', label: 'IMAP 端口', placeholder: '993', type: 'text'},
            {key: 'username', label: '用户名', placeholder: 'yourname@qq.com', type: 'text'},
            {key: 'password', label: '密码或授权码', placeholder: '邮箱授权码', type: 'password'},
            {key: 'from_address', label: '发件地址', placeholder: 'yourname@qq.com', type: 'text'},
            {key: 'from_name', label: '发件人名称', placeholder: 'AI助手', type: 'text'},
            {key: 'use_tls', label: '使用 TLS', type: 'checkbox'},
            {key: 'check_interval', label: '检查间隔', placeholder: '30', type: 'text'},
            {key: 'monitor_folder', label: '监控文件夹', placeholder: 'INBOX', type: 'text'}
        ]
    },
    telegram: {
        title: 'Telegram',
        helpText: 'openkitty-config.yaml: type: telegram, config.bot_token',
        fields: [
            {key: 'bot_token', label: 'Bot Token', placeholder: '123456:ABC-DEF...', type: 'password'}
        ]
    },
    weixin: {
        title: '微信 iLink',
        helpText: 'type: weixin。扫码连接后会自动写入 account_id/token。',
        fields: [
            {key: 'account_id', label: 'Account ID', placeholder: '扫码后自动填写', type: 'text'},
            {key: 'token', label: 'Token', placeholder: '扫码后自动填写', type: 'password'},
            {key: 'base_url', label: 'Base URL', placeholder: 'https://ilinkai.weixin.qq.com', type: 'text'},
            {key: 'bot_agent', label: 'Bot Agent', placeholder: 'MACS/1.0 OpenClawCompatible/2.4.6', type: 'text'},
            {key: 'allowed_users', label: '允许用户', placeholder: '多个用户用逗号分隔', type: 'text'}
        ]
    }
};

function settingNormalizeChannelType(type) {
    type = String(type || '').toLowerCase();
    if (type === 'dingding') return 'dingtalk';
    if (type === 'openclaw-weixin') return 'weixin';
    return type;
}

function settingChannelSchema(type) {
    return CHANNEL_SCHEMAS[settingNormalizeChannelType(type)] || null;
}

function settingChannelRequiredFields(type) {
    var map = {
        feishu: ['app_id', 'app_secret'],
        qq: ['app_id', 'app_secret'],
        dingtalk: ['client_id', 'client_secret', 'robot_code'],
        wework: ['bot_id', 'secret'],
        email: ['smtp_host', 'imap_host', 'username', 'password', 'from_address'],
        telegram: ['bot_token'],
        weixin: ['account_id', 'token']
    };
    return map[settingNormalizeChannelType(type)] || [];
}

function settingChannelTypeOptions(selected) {
    selected = settingNormalizeChannelType(selected);
    var known = [
        ['feishu', '飞书 (feishu)'],
        ['qq', 'QQ (qq)'],
        ['dingtalk', '钉钉 (dingtalk)'],
        ['telegram', 'Telegram (telegram)'],
        ['wework', '企业微信 (wework)'],
        ['weixin', '微信 iLink (weixin)'],
        ['email', '邮箱 (email)'],
        ['', '自定义 JSON']
    ];
    var html = '';
    for (var i = 0; i < known.length; i++) {
        html += '<option value="' + escapeAttr(known[i][0]) + '"' + (selected === known[i][0] ? ' selected' : '') + '>' + escapeHtml(known[i][1]) + '</option>';
    }
    return html;
}

function settingDefaultChannelConfig(type) {
    type = settingNormalizeChannelType(type);
    if (type === 'dingtalk') return {robot_code: ''};
    if (type === 'email') return {smtp_host: 'smtp.qq.com', smtp_port: 465, imap_host: 'imap.qq.com', imap_port: 993, from_name: 'AI助手', use_tls: true, check_interval: 30, monitor_folder: 'INBOX'};
    if (type === 'weixin') return {base_url: 'https://ilinkai.weixin.qq.com', bot_agent: 'MACS/1.0 OpenClawCompatible/2.4.6'};
    return {};
}

function settingDefaultChannelName(type) {
    type = settingNormalizeChannelType(type);
    return type || 'custom_channel';
}

function settingChannelEditorFieldsHTML(type, config, index) {
    type = settingNormalizeChannelType(type);
    config = Object.assign({}, settingDefaultChannelConfig(type), config || {});
    var schema = settingChannelSchema(type);
    if (!schema) {
        return '<div class="settings-editor-grid">' +
            settingsField('自定义类型', '<input type="text" id="channelEditCustomType" value="' + escapeHtml(type) + '" placeholder="例如 custom">', '', true) +
            settingsField('配置 JSON', '<textarea class="channel-config" rows="9">' + escapeHtml(JSON.stringify(config || {}, null, 2)) + '</textarea>', '自定义渠道会原样保存 JSON。', true) +
            '</div>';
    }
    var html = '';
    if (type === 'weixin') {
        html += '<div style="margin-bottom:12px;border:1px solid #bbf7d0;background:#f0fdf4;border-radius:8px;padding:12px;">' +
            '<button type="button" class="settings-action-btn success" onclick="startWeixinQrLogin(' + index + ')">扫码登录微信</button>' +
            '<span id="weixinQrStatus_' + index + '" style="margin-left:10px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;"></span>' +
            '<div id="weixinQrBox_' + index + '" style="margin-top:10px;"></div>' +
            '</div>';
    }
    if (schema.helpText) {
        html += '<details class="settings-details"><summary>' + escapeHtml(settingsText('settingsTechnicalNotes', '技术说明')) + '</summary><div class="settings-details-body config-hint">' + escapeHtml(schema.helpText) + '</div></details>';
    }
    html += '<div class="settings-editor-grid">';
    for (var i = 0; i < schema.fields.length; i++) {
        var field = schema.fields[i];
        var value = config[field.key] != null ? config[field.key] : '';
        var control = '';
        if (field.type === 'checkbox') {
            control = '<label style="display:flex;align-items:center;gap:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#334155;"><input type="checkbox" class="settings-channel-field channel-field-' + escapeAttr(field.key) + '" data-field-key="' + escapeAttr(field.key) + '" ' + (value ? 'checked' : '') + '> 开启</label>';
        } else {
            control = '<input type="' + escapeAttr(field.type || 'text') + '" class="settings-channel-field channel-field-' + escapeAttr(field.key) + '" data-field-key="' + escapeAttr(field.key) + '" value="' + escapeHtml(String(value)) + '" placeholder="' + escapeHtml(field.placeholder || '') + '">';
        }
        var full = field.key === 'allowed_users' || field.key === 'base_url' || field.key === 'bot_agent' || field.key === 'bot_token';
        html += settingsField(field.label || field.key, control, field.help || '', full);
    }
    html += '</div>';
    return html;
}

function refreshChannelEditorFields() {
    var modal = document.getElementById('settingsItemEditorModal');
    if (!modal) return;
    var index = parseInt(modal.getAttribute('data-ref') || '-1', 10);
    var type = settingNormalizeChannelType(document.getElementById('channelEditType')?.value || '');
    var channel = index >= 0 ? (currentConfig?.channels?.[index] || {}) : {};
    var sameType = settingNormalizeChannelType(channel.type || '') === type;
    var nameEl = document.querySelector('#settingsItemEditorBody .channel-name');
    if (nameEl && (!nameEl.value.trim() || nameEl.value === 'new_channel' || nameEl.value === 'custom_channel')) {
        nameEl.value = settingDefaultChannelName(type);
    }
    var fields = document.getElementById('channelEditorFields');
    if (fields) fields.innerHTML = settingChannelEditorFieldsHTML(type, sameType ? (channel.config || {}) : {}, index);
}

function openChannelEditor(index, draftChannel) {
    if (!currentConfig.channels) currentConfig.channels = [];
    var isNew = index == null || index < 0;
    var channel = isNew ? (draftChannel || {name: 'feishu', type: 'feishu', enabled: false, config: {}}) : currentConfig.channels[index];
    if (!channel) return;
    var type = settingNormalizeChannelType(channel.type || 'feishu');
    var body = '<div class="settings-editor-grid">' +
        settingsField('名称', '<input type="text" class="channel-name" data-index="' + index + '" value="' + escapeHtml(channel.name || settingDefaultChannelName(type)) + '">', '', false) +
        settingsField('类型', '<select id="channelEditType" class="channel-type" data-index="' + index + '" onchange="refreshChannelEditorFields()">' + settingChannelTypeOptions(type) + '</select>', '', false) +
        settingsField('启用连接', '<label style="display:flex;align-items:center;gap:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#334155;"><input type="checkbox" class="channel-enabled" data-index="' + index + '" ' + (channel.enabled ? 'checked' : '') + '> 接收这个应用中的任务</label>', '', true) +
        '</div>' +
        '<div id="channelEditorFields">' + settingChannelEditorFieldsHTML(type, channel.config || {}, isNew ? -1 : index) + '</div>';
    settingsOpenEditor('channel', isNew ? '连接应用' : '编辑应用连接', body, isNew ? -1 : index);
}

function commitChannelEditor(index) {
    if (!currentConfig.channels) currentConfig.channels = [];
    var body = document.getElementById('settingsItemEditorBody');
    var name = (body.querySelector('.channel-name')?.value || '').trim();
    if (!name) { alert('渠道名称不能为空'); return false; }
    var type = settingNormalizeChannelType(body.querySelector('.channel-type')?.value || '');
    var schema = settingChannelSchema(type);
    var config = {};
    if (schema) {
        var inputs = body.querySelectorAll('.settings-channel-field');
        for (var i = 0; i < inputs.length; i++) {
            var input = inputs[i];
            var key = input.getAttribute('data-field-key');
            if (!key) continue;
            if (input.type === 'checkbox') {
                config[key] = !!input.checked;
            } else {
                var value = input.value.trim();
                if (value) config[key] = (key === 'smtp_port' || key === 'imap_port' || key === 'check_interval') ? (parseInt(value, 10) || value) : value;
            }
        }
    } else {
        var customType = (body.querySelector('#channelEditCustomType')?.value || '').trim();
        if (customType) type = customType;
        var jsonText = body.querySelector('.channel-config')?.value || '{}';
        try {
            config = JSON.parse(jsonText || '{}');
        } catch (e) {
            alert('渠道 JSON 解析失败: ' + e.message);
            return false;
        }
    }
    var nextChannel = {
        name: name,
        type: type,
        enabled: !!body.querySelector('.channel-enabled')?.checked,
        config: config
    };
    if (index == null || index < 0) currentConfig.channels.push(nextChannel);
    else currentConfig.channels[index] = nextChannel;
    renderChannelsList();
    settingsSetDirty(true);
    return true;
}

function addChannel() {
    openChannelEditor(-1);
}

async function startWeixinQrLoginLegacyC(index) {
    if (!settingsApplyActiveEditor()) return;
    openChannelEditor(index);
    var channel = currentConfig?.channels?.[index] || {};
    var channelName = channel.name || 'weixin';
    var statusEl = document.getElementById('weixinQrStatus_' + index);
    var boxEl = document.getElementById('weixinQrBox_' + index);
    if (statusEl) statusEl.textContent = '正在生成二维码...';
    if (boxEl) boxEl.innerHTML = '';
    try {
        var response = await fetch('/api/weixin/login/start', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({channel: channelName, force: true})
        });
        var data = await response.json();
        if (!response.ok || !data.ok || !data.qr_data_url) {
            throw new Error((data && (data.error || data.message)) || '二维码生成失败');
        }
        if (statusEl) statusEl.textContent = '请用手机微信扫码确认';
        if (boxEl) {
            var qr = String(data.qr_data_url || '');
            var qrPrefix = qr.slice(0, 80);
            var qrHtml = '<div style="display:flex;align-items:flex-start;gap:12px;flex-wrap:wrap;">' +
                '<div>' +
                '<img id="weixinQrImage_' + index + '" src="' + escapeAttr(qr) + '" alt="Weixin QR" style="width:180px;height:180px;border:1px solid #e5e7eb;border-radius:8px;background:white;object-fit:contain;">' +
                '<div id="weixinQrImageError_' + index + '" style="display:none;width:220px;min-height:120px;border:1px dashed #fca5a5;border-radius:8px;padding:12px;color:#b91c1c;background:#fff5f5;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">二维码图片加载失败。返回长度: ' + qr.length + '<br>前缀: ' + escapeHtml(qrPrefix) + '</div>' +
                '</div>' +
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#475569;line-height:1.7;max-width:360px;">扫码后本机会保存 iLink Bot token 到渠道配置。建议保留 allowed_users，只允许扫码用户触发 AI 员工。</div>' +
                '</div>';
            boxEl.innerHTML = qrHtml;
            var img = document.getElementById('weixinQrImage_' + index);
            var errBox = document.getElementById('weixinQrImageError_' + index);
            if (img && errBox) {
                img.onerror = function() {
                    img.style.display = 'none';
                    errBox.style.display = 'block';
                };
            }
        }
        pollWeixinQrLogin(data.session_key, channelName, index);
    } catch (err) {
        if (statusEl) statusEl.textContent = '扫码启动失败: ' + err.message;
        if (boxEl) boxEl.innerHTML = '';
    }
}

async function startWeixinQrLogin(index) {
    index = parseInt(index, 10);
    if (isNaN(index)) index = -1;
    if (!currentConfig.channels) currentConfig.channels = [];

    var modal = document.getElementById('settingsItemEditorModal');
    var activeRef = modal ? parseInt(modal.getAttribute('data-ref') || '-1', 10) : -2;
    var activeKind = modal ? (modal.getAttribute('data-kind') || '') : '';
    var isSameChannelEditor = !!(modal && modal.classList.contains('active') && activeKind === 'channel' && activeRef === index);
    if (!isSameChannelEditor) {
        if (!settingsApplyActiveEditor()) return;
        if (index >= 0) {
            openChannelEditor(index);
        } else {
            openChannelEditor(-1, {
                name: 'weixin',
                type: 'weixin',
                enabled: true,
                config: settingDefaultChannelConfig('weixin')
            });
        }
    } else {
        var typeEl = document.getElementById('channelEditType');
        if (typeEl && settingNormalizeChannelType(typeEl.value || '') !== 'weixin') {
            typeEl.value = 'weixin';
            refreshChannelEditorFields();
        }
    }

    var body = document.getElementById('settingsItemEditorBody');
    var nameEl = body ? body.querySelector('.channel-name') : null;
    var channel = index >= 0 ? (currentConfig.channels[index] || {}) : {};
    var channelName = ((nameEl && nameEl.value) || channel.name || 'weixin').trim() || 'weixin';
    if (nameEl && !nameEl.value.trim()) nameEl.value = channelName;

    var statusEl = document.getElementById('weixinQrStatus_' + index);
    var boxEl = document.getElementById('weixinQrBox_' + index);
    if (!statusEl || !boxEl) {
        refreshChannelEditorFields();
        statusEl = document.getElementById('weixinQrStatus_' + index);
        boxEl = document.getElementById('weixinQrBox_' + index);
    }
    if (statusEl) statusEl.textContent = '正在生成二维码...';
    if (boxEl) {
        boxEl.innerHTML = '<div style="width:180px;height:180px;border:1px dashed #cbd5e1;border-radius:8px;display:flex;align-items:center;justify-content:center;color:#64748b;font-size:calc(13.2px * var(--macs-font-scale, 1));background:#f8fafc;">二维码生成中</div>';
    }

    try {
        var response = await fetch('/api/weixin/login/start', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({channel: channelName, force: true})
        });
        var data = await response.json().catch(function() { return {}; });
        if (!response.ok || !data.ok || !data.qr_data_url) {
            throw new Error((data && (data.error || data.message)) || '二维码生成失败');
        }
        if (statusEl) statusEl.textContent = '请用手机微信扫码确认';
        if (boxEl) {
            var qr = String(data.qr_data_url || '');
            var qrPrefix = qr.slice(0, 80);
            var qrHtml = '<div style="display:flex;align-items:flex-start;gap:12px;flex-wrap:wrap;">' +
                '<div>' +
                '<img id="weixinQrImage_' + index + '" src="' + escapeAttr(qr) + '" alt="Weixin QR" style="width:180px;height:180px;border:1px solid #e5e7eb;border-radius:8px;background:white;object-fit:contain;">' +
                '<div id="weixinQrImageError_' + index + '" style="display:none;width:220px;min-height:120px;border:1px dashed #fca5a5;border-radius:8px;padding:12px;color:#b91c1c;background:#fff5f5;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">二维码图片加载失败。返回长度: ' + qr.length + '<br>前缀: ' + escapeHtml(qrPrefix) + '</div>' +
                '</div>' +
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#475569;line-height:1.7;max-width:360px;">扫码后本机会保存 iLink Bot token 到渠道配置。建议保留 allowed_users，只允许扫码用户触发 AI 员工。</div>' +
                '</div>';
            boxEl.innerHTML = qrHtml;
            var img = document.getElementById('weixinQrImage_' + index);
            var errBox = document.getElementById('weixinQrImageError_' + index);
            if (img && errBox) {
                img.onerror = function() {
                    img.style.display = 'none';
                    errBox.style.display = 'block';
                };
            }
        }
        pollWeixinQrLogin(data.session_key, channelName, index);
    } catch (err) {
        if (statusEl) statusEl.textContent = '扫码启动失败: ' + err.message;
        if (boxEl) {
            boxEl.innerHTML = '<div style="border:1px dashed #fca5a5;border-radius:8px;padding:12px;color:#b91c1c;background:#fff5f5;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;">' + escapeHtml(err.message || '二维码生成失败') + '</div>';
        }
    }
}

function openWeixinQrLogin(index) {
    startWeixinQrLogin(index);
}

function addWeixinChannelAndLogin() {
    if (!currentConfig.channels) currentConfig.channels = [];
    var idx = findWeixinChannelIndex();
    if (idx >= 0) {
        startWeixinQrLogin(idx);
        return;
    }
    startWeixinQrLogin(-1);
}
