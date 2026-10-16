// SSE module - real-time events
// eventSource vars
	var progressTimelineTextLimit = 800;
	var progressStatusTextLimit = 240;

	function detailedExecutionUIEnabled() {
		try {
			if (typeof window.isDetailedExecutionUIEnabled === 'function') {
				return window.isDetailedExecutionUIEnabled();
			}
			return typeof isDebugMode === 'function' && isDebugMode();
		} catch (e) {
			return false;
		}
	}

	function userFacingProgressMessage(value, eventType, status) {
		var raw = String(value || '');
		if (!detailedExecutionUIEnabled()) {
			raw = typeof window.sanitizeAssistantBubbleContent === 'function'
				? window.sanitizeAssistantBubbleContent(raw)
				: raw
					.replace(/\.openkitty[\\/][^\s]+/gi, '[\u5185\u90e8\u8def\u5f84]')
					.replace(/[A-Za-z]:[\\/][^\s`"'<>)]*openkitty[\\/][^\s`"'<>)]*/gi, '[\u5185\u90e8\u8def\u5f84]');
		}
		raw = String(raw || '').trim();
		var fixed = {
			task_start: '\u4efb\u52a1\u5df2\u5f00\u59cb',
			llm_call: '\u6b63\u5728\u5206\u6790\u548c\u6574\u7406\u2026',
			compress: '\u6b63\u5728\u6574\u7406\u4e0a\u4e0b\u6587\u2026',
			error: '\u5f53\u524d\u6b65\u9aa4\u9047\u5230\u95ee\u9898\uff0c\u6b63\u5728\u8c03\u6574\u2026',
			complete: '\u5904\u7406\u5b8c\u6210'
		};
		if (eventType === 'thinking') {
			return detailedExecutionUIEnabled() && raw ? raw : '\u6b63\u5728\u601d\u8003\u2026';
		}
		if (eventType === 'tool_start') {
			return raw || '\u6b63\u5728\u8c03\u7528\u5de5\u5177\u2026';
		}
		if (eventType === 'tool_end') {
			return raw || (status === 'success'
				? '\u6b65\u9aa4\u5df2\u5b8c\u6210'
				: '\u5f53\u524d\u6b65\u9aa4\u672a\u5b8c\u6210\uff0c\u6b63\u5728\u5c1d\u8bd5\u5176\u4ed6\u65b9\u5f0f\u2026');
		}
		if (fixed[eventType]) return eventType === 'task_start' && raw ? raw : fixed[eventType];
		return raw || '\u6b63\u5728\u5904\u7406\u2026';
	}
	window.userFacingProgressMessage = userFacingProgressMessage;

	function truncateProgressText(value, limit) {
		var text = value == null ? '' : String(value);
		if (!limit || limit < 1) return '';
		var chars = Array.from(text);
		return chars.length > limit ? chars.slice(0, limit).join('') + '...' : text;
	}

	function formatTaskClockTime(eventTs) {
		var timeValue = eventTs || Date.now();
		if (typeof timeValue === 'string') {
			var parsed = Date.parse(timeValue);
			timeValue = isNaN(parsed) ? Date.now() : parsed;
		}
		var date = new Date(timeValue);
		if (isNaN(date.getTime())) date = new Date();
		function pad(n) { return n < 10 ? '0' + n : '' + n; }
		return pad(date.getMinutes()) + ':' + pad(date.getSeconds());
	}

	function normalizedProgressToolName(value) {
		return String(value || '')
			.trim()
			.replace(/^functions[.:/_-]+/i, '')
			.replace(/^mcp__[^_]+__/i, '')
			.replace(/^tools[.:/_-]+/i, '');
	}

	function progressToolPresentation(value) {
		var toolName = normalizedProgressToolName(value);
		var key = toolName.toLowerCase();
		if (/^(?:cli|bash|shell|shell_command|exec|exec_command|command|powershell|terminal)$/.test(key)) {
			return { kind: 'terminal', label: '\u8fd0\u884c\u547d\u4ee4' };
		}
		if (/^(?:read|read_file|view_file|list_dir|listdir|glob)$/.test(key)) {
			return { kind: 'read', label: key === 'list_dir' || key === 'listdir' || key === 'glob' ? '\u67e5\u627e\u6587\u4ef6' : '\u8bfb\u53d6\u6587\u4ef6' };
		}
		if (/^(?:grep|search_file|search_files|code_search)$/.test(key)) {
			return { kind: 'search', label: '\u641c\u7d22\u5185\u5bb9' };
		}
		if (/^(?:write|write_file|edit|edit_file|apply_patch|json_patch|multiedit|multi_edit)$/.test(key)) {
			return { kind: 'edit', label: /^(?:write|write_file)$/.test(key) ? '\u5199\u5165\u6587\u4ef6' : '\u7f16\u8f91\u6587\u4ef6' };
		}
		if (/^(?:web_search|search|search_query|exa|deep_search)$/.test(key)) {
			return { kind: 'search', label: '\u641c\u7d22\u7f51\u9875' };
		}
		if (/^(?:web_fetch|fetch|browser|browse|open_url|http|get_url)$/.test(key)) {
			return { kind: 'web', label: '\u8bbf\u95ee\u7f51\u9875' };
		}
		if (/^(?:use_skill|skill|skill_call)$/.test(key)) {
			return { kind: 'skill', label: '\u8c03\u7528\u6280\u80fd' };
		}
		if (/^(?:vision|imageview|image_view|view_image)$/.test(key)) {
			return { kind: 'image', label: '\u67e5\u770b\u56fe\u7247' };
		}
		if (/^(?:imagegeneration|image_generation|imagegen|generate_image)$/.test(key)) {
			return { kind: 'image', label: '\u751f\u6210\u56fe\u7247' };
		}
		if (/^(?:plan_task|update_plan|update_plan_step)$/.test(key)) {
			return { kind: 'plan', label: '\u66f4\u65b0\u4efb\u52a1\u8ba1\u5212' };
		}
		if (/^(?:git|git_tool)$/.test(key)) {
			return { kind: 'git', label: '\u6267\u884c Git \u64cd\u4f5c' };
		}
		var displayName = toolName.replace(/[_-]+/g, ' ').trim();
		return { kind: 'tool', label: displayName ? '\u8c03\u7528 ' + displayName : '\u6267\u884c\u64cd\u4f5c' };
	}

	function progressIconSvg(kind) {
		var paths = {
			terminal: '<path d="m4 6 4 4-4 4M10 15h6"/>',
			read: '<path d="M5 3.5h7l3 3V16.5H5z"/><path d="M12 3.5v3h3M7.5 10h5M7.5 13h4"/>',
			edit: '<path d="m4 15.5.8-3.4 7.8-7.8 3.1 3.1-7.8 7.8zM11.5 5.4l3.1 3.1"/>',
			search: '<circle cx="8.5" cy="8.5" r="4.5"/><path d="m12 12 4 4"/>',
			web: '<circle cx="10" cy="10" r="7"/><path d="M3 10h14M10 3a11 11 0 0 1 0 14M10 3a11 11 0 0 0 0 14"/>',
			skill: '<path d="m10 2 1.2 4.2L15 5l-1.2 3.8L18 10l-4.2 1.2L15 15l-3.8-1.2L10 18l-1.2-4.2L5 15l1.2-3.8L2 10l4.2-1.2L5 5l3.8 1.2z"/>',
			image: '<rect x="3" y="4" width="14" height="12" rx="2"/><circle cx="7.5" cy="8" r="1.2"/><path d="m5 14 3.2-3.2 2.3 2.3 1.8-1.8L15 14"/>',
			plan: '<rect x="4" y="3" width="12" height="14" rx="2"/><path d="M7 7h6M7 10h6M7 13h4"/>',
			git: '<circle cx="6" cy="5" r="2"/><circle cx="14" cy="15" r="2"/><path d="M6 7v3c0 2.8 2.2 5 5 5h1M6 10h4a3 3 0 0 0 3-3V5"/>',
			brain: '<path d="M8 4a3 3 0 0 0-5 2 3 3 0 0 0 1 5 3 3 0 0 0 4 4M12 4a3 3 0 0 1 5 2 3 3 0 0 1-1 5 3 3 0 0 1-4 4M10 3v14M7 8h3M10 12h3"/>',
			tool: '<path d="M12.5 3.5a4 4 0 0 0-4.8 5L3 13.2 6.8 17l4.7-4.7a4 4 0 0 0 5-4.8l-2.7 2.7-2.2-2.2z"/>'
		};
		return '<svg viewBox="0 0 20 20" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.55" stroke-linecap="round" stroke-linejoin="round">' + (paths[kind] || paths.tool) + '</svg>';
	}

	function progressStateSvg(state) {
		if (state === 'running') return '<span class="progress-state-spinner" aria-hidden="true"></span>';
		if (state === 'success') return '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="m3.2 8.2 3 3 6.6-6.6"/></svg>';
		if (state === 'error') return '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="m4 4 8 8M12 4l-8 8"/></svg>';
		if (state === 'warning') return '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 2.3 14 13H2zM8 6v3.3M8 11.5v.1"/></svg>';
		return '<svg viewBox="0 0 16 16" aria-hidden="true"><circle cx="8" cy="8" r="5.5"/><path d="M8 5v3.4l2.2 1.4"/></svg>';
	}

	function progressEventState(data) {
		var type = String(data && data.type || '').toLowerCase();
		var status = String(data && data.status || '').toLowerCase();
		if (type === 'error' || type === 'stage_error' || status === 'error' || status === 'failed') return 'error';
		if (status === 'warning' || status === 'degraded') return 'warning';
		if (status === 'waiting_approval' || type === 'stage_waiting' || status === 'waiting') return 'waiting';
		if (type === 'tool_end' || type === 'complete' || type === 'stage_done' || status === 'success' || status === 'completed') return 'success';
		return 'running';
	}

	function escapeProgressRegExp(value) {
		return String(value || '').replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	}

	function progressMessageDetail(message, toolName, eventType) {
		var detail = String(message || '').trim();
		detail = detail.replace(/^(?:\ud83d\ude80|\ud83d\udd27|\u2705|\u274c|\ud83e\udd16|\ud83d\udce6|\u26a0\ufe0f?|\ud83c\udfaf|\u26a1|\ud83d\udcd6|\u270f\ufe0f?|\u2702\ufe0f?|\ud83d\udd0d|\ud83c\udf10|\ud83e\udde0|\ud83d\udd39)\s*/u, '');
		if (toolName) {
			var toolPattern = new RegExp('^(?:will use\\s+)?' + escapeProgressRegExp(toolName) + '(?:\\s+|\\b)', 'i');
			detail = detail.replace(toolPattern, '').trim();
		}
		if (eventType === 'tool_end') {
			detail = detail.replace(/^completed\b\s*/i, '').replace(/^failed:\s*/i, '').trim();
		}
		detail = detail.replace(/^[\u2190\u2192]\s*/, '').trim();
		if (/^(?:\u6b65\u9aa4\u5df2\u5b8c\u6210|\u6b63\u5728\u8c03\u7528\u5de5\u5177\u2026?|\u6b63\u5728\u601d\u8003\u2026?|\u6b63\u5728\u5206\u6790\u548c\u6574\u7406\u2026?|working|tool complete)$/i.test(detail)) return '';
		return detail;
	}

	function progressEventModel(data, message) {
		data = data || {};
		var type = String(data.type || 'progress').toLowerCase();
		var state = progressEventState(data);
		var toolName = normalizedProgressToolName(data.tool || data.tool_name || '');
		var presentation = progressToolPresentation(toolName);
		var detail = progressMessageDetail(message, toolName, type);
		var title = '';
		var kind = presentation.kind;
		if (type === 'tool_start' || type === 'tool_end' || toolName) {
			title = presentation.label;
		} else if (type === 'llm_call' || type === 'thinking') {
			kind = 'brain';
			title = type === 'thinking' ? '\u6b63\u5728\u601d\u8003' : '\u5206\u6790\u5e76\u6574\u7406\u4fe1\u606f';
		} else if (type === 'compress') {
			kind = 'plan';
			title = '\u6574\u7406\u4e0a\u4e0b\u6587';
		} else if (type === 'task_start') {
			kind = 'plan';
			title = detail || '\u4efb\u52a1\u5df2\u5f00\u59cb';
			detail = '';
		} else if (type === 'complete') {
			kind = 'plan';
			title = detail || '\u5904\u7406\u5b8c\u6210';
			detail = '';
		} else if (type.indexOf('stage_') === 0) {
			kind = 'plan';
			title = detail || '\u6267\u884c\u4efb\u52a1\u9636\u6bb5';
			detail = '';
		} else if (type === 'error') {
			kind = 'tool';
			title = '\u6267\u884c\u9047\u5230\u95ee\u9898';
		} else {
			title = detail || '\u6b63\u5728\u5904\u7406';
			detail = '';
		}

		var currentText = title;
		if (type === 'tool_start') currentText = '\u6b63\u5728' + title + '\u2026';
		if (type === 'tool_end') currentText = title + (state === 'error' ? '\u5931\u8d25' : '\u5df2\u5b8c\u6210');
		return {
			type: type,
			state: state,
			kind: kind,
			title: truncateProgressText(title, 80),
			preview: truncateProgressText(detail, 160),
			rawDetail: truncateProgressText(message, progressTimelineTextLimit),
			currentText: truncateProgressText(currentText, progressStatusTextLimit),
			toolName: toolName
		};
	}

	function findProgressBubbleByToolCallID(timeline, toolCallID) {
		if (!timeline || !toolCallID) return null;
		var rows = timeline.querySelectorAll('.progress-bubble[data-tool-call-id]');
		for (var i = 0; i < rows.length; i++) {
			if (rows[i].getAttribute('data-tool-call-id') === String(toolCallID)) return rows[i];
		}
		return null;
	}

	function setProgressBubbleState(row, state) {
		row.setAttribute('data-progress-state', state);
		var stateEl = row.querySelector('.progress-bubble-state');
		if (!stateEl) return;
		stateEl.className = 'progress-bubble-state is-' + state;
		stateEl.innerHTML = progressStateSvg(state);
		stateEl.setAttribute('aria-label', state === 'success' ? '\u5df2\u5b8c\u6210' : state === 'error' ? '\u6267\u884c\u5931\u8d25' : state === 'warning' ? '\u9700\u8981\u6ce8\u610f' : state === 'waiting' ? '\u7b49\u5f85\u786e\u8ba4' : '\u6267\u884c\u4e2d');
	}

	function appendProgressDetail(row, detail) {
		detail = String(detail || '').trim();
		if (!detail) return;
		row._progressDetails = row._progressDetails || [];
		if (row._progressDetails.indexOf(detail) === -1) row._progressDetails.push(detail);
		var pre = row.querySelector('.progress-bubble-detail pre');
		if (pre) pre.textContent = row._progressDetails.join('\n');
	}

	function createProgressBubble(model, data, clockLabel) {
		var row = document.createElement('details');
		row.className = 'progress-bubble';
		var summary = document.createElement('summary');
		summary.className = 'progress-bubble-summary';

		var icon = document.createElement('span');
		icon.className = 'progress-bubble-icon is-' + model.kind;
		icon.innerHTML = progressIconSvg(model.kind);

		var copy = document.createElement('span');
		copy.className = 'progress-bubble-copy';
		var title = document.createElement('span');
		title.className = 'progress-bubble-title';
		title.textContent = model.title;
		var preview = document.createElement('span');
		preview.className = 'progress-bubble-preview';
		preview.textContent = model.preview;
		if (!model.preview) preview.hidden = true;
		copy.appendChild(title);
		copy.appendChild(preview);

		var time = document.createElement('time');
		time.className = 'progress-bubble-time';
		time.textContent = clockLabel;
		var state = document.createElement('span');
		state.className = 'progress-bubble-state';
		var chevron = document.createElement('span');
		chevron.className = 'progress-bubble-chevron';
		chevron.innerHTML = '<svg viewBox="0 0 12 12" aria-hidden="true"><path d="m3 4.5 3 3 3-3"/></svg>';

		summary.appendChild(icon);
		summary.appendChild(copy);
		summary.appendChild(time);
		summary.appendChild(state);
		summary.appendChild(chevron);
		row.appendChild(summary);

		var detail = document.createElement('div');
		detail.className = 'progress-bubble-detail';
		var pre = document.createElement('pre');
		detail.appendChild(pre);
		row.appendChild(detail);
		setProgressBubbleState(row, model.state);
		appendProgressDetail(row, model.rawDetail || model.preview || model.title);
		return row;
	}

	function progressAggregateText(counts) {
		var definitions = [
			['terminal', '\u8fd0\u884c {n} \u6761\u547d\u4ee4'],
			['read', '\u8bfb\u53d6 {n} \u4e2a\u6587\u4ef6'],
			['edit', '\u7f16\u8f91 {n} \u4e2a\u6587\u4ef6'],
			['search', '\u5b8c\u6210 {n} \u6b21\u641c\u7d22'],
			['web', '\u8bbf\u95ee {n} \u4e2a\u7f51\u9875'],
			['skill', '\u8c03\u7528 {n} \u4e2a\u6280\u80fd'],
			['image', '\u5904\u7406 {n} \u5f20\u56fe\u7247'],
			['plan', '\u66f4\u65b0 {n} \u6b21\u8ba1\u5212'],
			['git', '\u6267\u884c {n} \u6b21 Git \u64cd\u4f5c'],
			['tool', '\u6267\u884c {n} \u9879\u64cd\u4f5c']
		];
		var parts = [];
		for (var i = 0; i < definitions.length; i++) {
			var count = counts[definitions[i][0]] || 0;
			if (count > 0) parts.push(definitions[i][1].replace('{n}', count));
			if (parts.length === 3) break;
		}
		return parts.join(' \u00b7 ');
	}

	function updateProgressAggregate(loadingEl, model, data, rowCount) {
		if (!loadingEl || model.type !== 'tool_start') return;
		loadingEl._progressSeenCalls = loadingEl._progressSeenCalls || {};
		var eventKey = String(data.tool_call_id || data.call_id || model.toolName + ':' + (data.ts || '') + ':' + model.rawDetail);
		if (!loadingEl._progressSeenCalls[eventKey]) {
			loadingEl._progressSeenCalls[eventKey] = true;
			loadingEl._progressCounts = loadingEl._progressCounts || {};
			loadingEl._progressCounts[model.kind] = (loadingEl._progressCounts[model.kind] || 0) + 1;
		}
		var summary = loadingEl.querySelector('.log-summary');
		if (!summary) return;
		var count = rowCount || loadingEl.querySelectorAll('.progress-bubble').length;
		var baseText = progressAggregateText(loadingEl._progressCounts || {});
		summary._progressBaseText = baseText;
		summary.hidden = count <= 4;
		if (count <= 4) return;
		var expanded = loadingEl.classList.contains('is-log-expanded');
		summary.textContent = baseText + (expanded ? ' \u00b7 \u6536\u8d77' : ' \u00b7 \u5c55\u5f00\u5168\u90e8');
		summary.setAttribute('aria-expanded', expanded ? 'true' : 'false');
	}

	function ensureProgressSummaryBehavior(loadingEl) {
		var summary = loadingEl && loadingEl.querySelector('.log-summary');
		if (!summary || summary._progressBound) return;
		summary._progressBound = true;
		summary.addEventListener('click', function() {
			loadingEl.classList.toggle('is-log-expanded');
			var expanded = loadingEl.classList.contains('is-log-expanded');
			var baseText = summary._progressBaseText || '';
			summary.textContent = baseText + (expanded ? ' \u00b7 \u6536\u8d77' : ' \u00b7 \u5c55\u5f00\u5168\u90e8');
			summary.setAttribute('aria-expanded', expanded ? 'true' : 'false');
		});
	}

	function renderProgressEventBubble(loadingEl, data) {
		data = data || {};
		if (!loadingEl) return null;
		var rawMessage = data.message || '';
		if (typeof filterDebugMsg === 'function') rawMessage = filterDebugMsg(rawMessage);
		rawMessage = stripEmbeddedTaskClock(rawMessage);
		var message = userFacingProgressMessage(rawMessage, data.type, data.status);
		var model = progressEventModel(data, message);
		var timeline = loadingEl.querySelector('.log-stream');
		if (!timeline) return null;
		timeline.style.display = 'grid';
		var showDetailedProgress = detailedExecutionUIEnabled();
		var exportBtn = loadingEl.querySelector('.log-export-btn');
		if (exportBtn) exportBtn.style.display = showDetailedProgress ? 'block' : 'none';
		ensureProgressSummaryBehavior(loadingEl);

		var callID = data.tool_call_id || data.call_id || '';
		var row = model.type === 'tool_end' ? findProgressBubbleByToolCallID(timeline, callID) : null;
		if (!row) {
			row = createProgressBubble(model, data, formatTaskClockTime(data.ts));
			timeline.appendChild(row);
		}
		row.setAttribute('data-progress-type', model.type || 'progress');
		row.setAttribute('data-progress-detail', showDetailedProgress ? 'full' : 'safe');
		row.setAttribute('data-progress-kind', model.kind);
		if (callID) row.setAttribute('data-tool-call-id', String(callID));
		setProgressBubbleState(row, model.state);
		appendProgressDetail(row, model.rawDetail);
		if (model.type === 'tool_end' && model.preview) {
			var preview = row.querySelector('.progress-bubble-preview');
			if (preview && model.state === 'error') {
				preview.hidden = false;
				preview.textContent = model.preview;
			}
		}

		var previousLatest = timeline.querySelector('.progress-bubble.latest');
		if (previousLatest && previousLatest !== row) previousLatest.classList.remove('latest');
		row.classList.add('latest');
		updateProgressAggregate(loadingEl, model, data, timeline.querySelectorAll('.progress-bubble').length);
		timeline.scrollTop = timeline.scrollHeight;

		var textSpan = loadingEl.querySelector('.loading-text');
		if (textSpan && model.currentText) textSpan.textContent = model.currentText;
		if (typeof scrollToBottom === 'function') scrollToBottom();
		return row;
	}
	window.renderProgressEventBubble = renderProgressEventBubble;

	// Older backend progress messages already include HH:mm:ss. The timeline
	// renders the structured event timestamp, so remove that legacy prefix to
	// avoid showing the same clock twice. Messages without a prefix are kept.
	function stripEmbeddedTaskClock(message) {
		return String(message || '').replace(/^\s*\[?\d{1,2}:\d{2}:\d{2}\]?\s+/, '');
	}

	function isUnavailableProgressLog(text) {
		if (!text) return true;
		var trimmed = String(text).trim();
		return trimmed === '(log not available - server restarted)' || trimmed === 'log not available - server restarted';
	}

	// Render a failed task exactly once across global SSE, per-task SSE and
	// polling fallbacks. A failed task can still contain a useful grounded
	// draft, so preserve that result and append a clear partial-result warning.
	window.renderFailedTaskResult = function(data, explicitLoadingId) {
		data = data || {};
		var taskId = data.task_id || data.id || '';
		window._renderedFailedTasks = window._renderedFailedTasks || {};
		var cleanup = function() {
			if (typeof clearPendingTask === 'function' && taskId) clearPendingTask(taskId);
			else if (window.pendingTasks && taskId) delete window.pendingTasks[taskId];
			if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();
		};
		if (taskId && window._renderedFailedTasks[taskId]) {
			cleanup();
			return false;
		}

		var failedResult = data.result == null ? '' :
			(typeof data.result === 'string' ? data.result : JSON.stringify(data.result, null, 2));
		var stopped = data.status === 'stopped';
		var internalReport = typeof window.looksLikeInternalExecutionReport === 'function' &&
			window.looksLikeInternalExecutionReport(failedResult);
		var safePartial = (!internalReport && failedResult && typeof window.sanitizeAssistantBubbleContent === 'function')
			? window.sanitizeAssistantBubbleContent(failedResult)
			: '';
		var failedContent = safePartial
			? safePartial + (stopped
				? '\n\n> 任务已停止。'
				: '\n\n> 任务未完全完成，可重试。')
			: (stopped ? '任务已停止。' : '处理未完成，请重试。');
		var loadingId = explicitLoadingId || (window.pendingTasks ? window.pendingTasks[taskId] : null);
		var loadingEl = loadingId ? document.getElementById(loadingId) : null;
		var taskSessionId = data.session_id ||
			(typeof getTaskSession === 'function' && taskId ? getTaskSession(taskId) : '');
		var hasCurrentSession = typeof currentSessionId !== 'undefined' && !!currentSessionId;
		var canAppendHere = !taskSessionId || (hasCurrentSession && currentSessionId === taskSessionId);
		var rendered = null;

		if (typeof renderMessage === 'function') {
			var holder = document.createElement('div');
			holder.innerHTML = renderMessage({
				role: 'assistant',
				content: failedContent,
				created_at: new Date().toISOString(),
				agent_name: failedResult ? (data.agent_name || '助手') : '系统',
				task_id: taskId
			});
			rendered = holder.firstElementChild;
		}
		if (!rendered) {
			rendered = document.createElement('div');
			rendered.className = 'message assistant';
			var safeContent = typeof escapeHtml === 'function' ? escapeHtml(failedContent) : failedContent;
			rendered.innerHTML = '<div class="message-bubble" style="background:#fff3cd;color:#856404;">' +
				'<div class="agent-name">系统</div><div>' + safeContent + '</div></div>';
		}

		var didRender = false;
		if (canAppendHere && loadingEl && loadingEl.parentNode) {
			loadingEl.parentNode.replaceChild(rendered, loadingEl);
			didRender = true;
		} else if (canAppendHere) {
			var messagesArea = document.getElementById('messagesArea');
			if (messagesArea) {
				if (messagesArea.innerHTML.indexOf('welcomeTips') !== -1) messagesArea.innerHTML = '';
				messagesArea.appendChild(rendered);
				didRender = true;
			}
		} else {
			if (loadingEl && loadingEl.parentNode) loadingEl.remove();
			if (typeof loadSessions === 'function') {
				try { loadSessions(); } catch(e) {}
			}
		}

		if (didRender && taskId) window._renderedFailedTasks[taskId] = true;
		cleanup();
		if (didRender && typeof scrollToBottom === 'function') scrollToBottom();
		return didRender;
	};

// loading watchdog
		var _loadingWatchdog = setInterval(function() {
			if (typeof isLoading === 'undefined' || !isLoading) return;
			var idle = Date.now() - (window._lastProgress || Date.now());
			var idleSec = Math.floor(idle / 1000);
			// Update loading text to show idle seconds after 15s
			var texts = document.querySelectorAll(".loading-text");
			texts.forEach(function(t) {
				if (idle >= 15000) {
					t.textContent = "处理中，已等待 " + idleSec + " 秒...";
				}
			});
			// Show refresh hint after 60s, auto-reset loading after 120s
			if (idle > 120000) {
				console.log("Loading watchdog: 超时 120s，自动复位");
				if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();
			} else if (idle > 60000) {
				var hints = document.querySelectorAll(".loading-refresh-hint");
				hints.forEach(function(h) { h.style.display = "block"; });
			}
		}, 5000);

// initSSE
	function initSSE() {

		if (eventSource) {

			eventSource.close();

		}

		const clientId = localStorage.getItem('sse_client_id') || 

						'client_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);

		localStorage.setItem('sse_client_id', clientId);

		eventSource = new EventSource('/api/events?client_id=' + clientId);

		eventSource.addEventListener('connected', function(e) {

			const data = JSON.parse(e.data);

			console.log('SSE 连接成功:', data);

			reconnectAttempts = 0;

			addLog('info', '实时推送已连接');

			// 重连后刷新会话，确保不遗漏已完成的任务
			if (currentSessionId && isLoading) {
				try { loadSession(currentSessionId); } catch(e) {}
				// 延迟再试一次，等后端完成写入
				setTimeout(function() {
					if (isLoading && currentSessionId) {
						try { loadSession(currentSessionId); } catch(e) {}
						// 如果 3 秒后还在 loading，强制复位
						setTimeout(function() {
							if (isLoading) {
								console.log('SSE 重连后仍未收到完成事件，强制复位');
								if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();
							}
						}, 3000);
					}
				}, 1500);
			}

		});

		eventSource.addEventListener('session_list_changed', function(e) {
			console.log('session list changed, refreshing...');
			if (typeof loadSessions === 'function') {
				loadSessions();
			}
		});

		eventSource.addEventListener('task_update', function(e) {

			const data = JSON.parse(e.data);
			if (data.task_id && data.session_id) {
				window.taskSessions = window.taskSessions || {};
				window.taskSessions[data.task_id] = data.session_id;
			}

			console.log('任务更新:', data);

			// 更新任务列表

			if (typeof loadTasks === 'function') {

				loadTasks();

			}

			// 查找对应的 loading 消息

			const loadingId = window.pendingTasks ? window.pendingTasks[data.task_id] : null;

			const loadingEl = loadingId ? document.getElementById(loadingId) : null;

			if (data.status === 'running') {

				addLog('info', '任务 ' + data.task_id + ' 开始执行');

				if (loadingEl) {

					// 更新 loading 动画的文字

					var textSpan = loadingEl.querySelector('.loading-text');

					if (textSpan) {

						textSpan.textContent = '⚡ 执行中...';

					}

					// 可选：添加进度提示

					var wrapper = loadingEl.querySelector('.typing-wrapper');

					if (wrapper && !wrapper.querySelector('.progress-hint')) {

						var hint = document.createElement('div');

						hint.className = 'progress-hint';

						hint.style.cssText = 'font-size: calc(11px * var(--macs-font-scale, 1)); color: #999; margin-top: 4px;';

						hint.textContent = '⚙️ 任务执行中';

						wrapper.appendChild(hint);

					}

				}

			} else if (data.status === 'completed' || data.status === 'degraded') {

				// 同步复位，确保多标签场景每个页面都清除 loading
				if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();

				addLog(
					data.status === 'degraded' ? 'warning' : 'info',
					'任务 ' + data.task_id + (data.status === 'degraded' ? ' 降级完成（结果可能不完整）' : ' 已完成')
				);

				window._lastDuration = (data.duration_sec || 0).toFixed(1) + 's';

				window._handledTask = true;
					window._lastCompletedTaskId = data.task_id;
					try { localStorage.setItem('lastCompletedTaskId', data.task_id); localStorage.setItem('lastDuration', window._lastDuration); } catch(e) {}



			// 移除 loading 动画

			if (loadingEl) {

				var bubbles = loadingEl.querySelectorAll(".progress-bubble");

				var logs = [];

				bubbles.forEach(function(b) { logs.push(b.textContent.trim()); });

					window._lastProgressLog = logs.length > 0 ? logs.join("\n") : null;

			}

				if (loadingEl && loadingEl.parentNode) {
					// 注入日志下载链接，用当前消息的 task_id，不用全局变量
					var msgBubble = loadingEl.closest('.message-bubble');
					if (msgBubble && detailedExecutionUIEnabled()) {
						var infoEl = msgBubble.querySelector('.message-info');
						if (!infoEl) {
							infoEl = document.createElement('div');
							infoEl.className = 'message-info';
							msgBubble.appendChild(infoEl);
						}
						var logTaskId = window.resolveProgressLogTaskId ? window.resolveProgressLogTaskId(data.task_id, data.session_id) : data.task_id;
						var logHref = window.progressLogDownloadUrl ? window.progressLogDownloadUrl(data.task_id, data.session_id) : '/api/task/progress-log/' + encodeURIComponent(logTaskId);
						infoEl.innerHTML += ' <a href="' + escapeHtml(logHref) + '" download="progress_' + escapeHtml(logTaskId) + '.log" style="margin-left:4px;color:#28a745;text-decoration:none;font-size:calc(12.1px * var(--macs-font-scale, 1));">&#x4e0b;&#x8f7d;&#x65e5;&#x5fd7;</a>';
					}
					loadingEl.remove();
				}

				// 清理映射

				var completedSessionId = data.session_id || (typeof getTaskSession === 'function' ? getTaskSession(data.task_id) : '');
				if (window.pendingTasks) {

					if (typeof clearPendingTask === 'function') clearPendingTask(data.task_id);
					else delete window.pendingTasks[data.task_id];

				}

// Refresh session after backend saves the final assistant message.

			(async function() {

				try {

					if (currentSessionId && (!completedSessionId || currentSessionId === completedSessionId)) {
						await loadSession(currentSessionId);
					} else if (typeof loadSessions === 'function') {
						loadSessions();
					}

				} catch(e) { console.error(e); }

				if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();

				// If result didn't appear, retry after delays (backend may still be saving)
				// Retry session load once if result not yet visible
				setTimeout(async function() {
					var lastMsg = document.querySelector('#messagesArea .message.assistant:last-child .message-content');
					if (!lastMsg || lastMsg.textContent.trim().length < 10) {
						console.log('Result not visible, retrying session load...');
						try { await loadSession(currentSessionId); } catch(e) {}
					}
				}, 2000);

			})();

		} else if (data.status === 'paused') {
			if (loadingEl && loadingEl.parentNode) loadingEl.remove();
			if (typeof clearPendingTask === 'function') clearPendingTask(data.task_id);
			else if (window.pendingTasks) delete window.pendingTasks[data.task_id];
			if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();
			if (typeof showToast === 'function') showToast('任务已暂停，可在工作中心继续', 'success');
		} else if (data.status === 'failed' || data.status === 'stopped') {

				addLog(data.status === 'stopped' ? 'info' : 'error', '任务 ' + data.task_id +
					(data.status === 'stopped' ? ' 已停止' : ' 失败: ' + (data.error || '未知错误')));
				window.renderFailedTaskResult(data, loadingId);

			}

			// 其他会话的任务更新 - 只有不是当前任务时才刷新

			if ((data.status === 'completed' || data.status === 'degraded') && !window._handledTask && (!window.pendingTasks || !window.pendingTasks[data.task_id])) {

				if (currentSessionId) {

					console.log('其他任务完成，刷新会话:', currentSessionId);

					loadSession(currentSessionId);

				}

			}

		});



		eventSource.onerror = function(e) {

			console.error('SSE 连接错误:', e);

			addLog('error', '实时推送连接断开，尝试重连...');

			if (window.reportBackendConnectionError) {
				window.reportBackendConnectionError('sse');
			}

			if (eventSource) {

				eventSource.close();

			}

			reconnectAttempts++;
			var delay = Math.min(30000, 3000 * Math.min(reconnectAttempts, 10));
			setTimeout(initSSE, delay);

		};

	}

// updateLoadingMessageForTask
	function updateLoadingMessageForTask(taskId, message) {

		const loadingId = window.pendingTasks ? window.pendingTasks[taskId] : null;

		if (loadingId) {

			const loadingEl = document.getElementById(loadingId);

			if (loadingEl) {

				const textSpan = loadingEl.querySelector('.loading-text');

				if (textSpan) {

					textSpan.textContent = userFacingProgressMessage(message, '', '');

				}

			}

		}

	}

// updateLoadingMessage
	function updateLoadingMessage(message) {

		if (currentLoadingId) {

			const loadingEl = document.getElementById(currentLoadingId);

			if (loadingEl) {

				// 更新 loading 动画，添加文字

				loadingEl.innerHTML = '<div class="message-bubble">' +

					'<div style="display: flex; align-items: center; gap: 8px;">' +

					'<div class="typing-indicator" style="display: inline-flex;"><span></span><span></span><span></span></div>' +

					'<span style="font-size: calc(13.2px * var(--macs-font-scale, 1)); color: #6c757d;">' + escapeHtml(userFacingProgressMessage(message, '', '')) + '</span>' +

					'</div>' +

					'</div>';

			}

		}

	}	

// restorePendingTasks
	async function restorePendingTasks(sessionId) {

		try {

			var resp = await fetch('/api/tasks?session_id=' + encodeURIComponent(sessionId));

			var data = await resp.json();

			var tasks = data.tasks || [];

			var runningTasks = tasks.filter(function(t) {
				if (!(t.status === 'running' || t.status === 'pending')) return false;
				if (sessionId && t.session_id && t.session_id !== sessionId) return false;
				return true;
			});
			runningTasks.sort(function(a, b) {
				return new Date(b.created_at || 0).getTime() - new Date(a.created_at || 0).getTime();
			});
			if (runningTasks.length > 1) {
				runningTasks = [runningTasks[0]];
			}

			if (runningTasks.length === 0) return;



			var container = document.getElementById('messagesArea');

			for (var i = 0; i < runningTasks.length; i++) {

				var t = runningTasks[i];

				var loadingId = 'loading_' + t.id;

				// Skip if already exists

				if (document.getElementById(loadingId)) continue;



				var agentName = document.getElementById('agentSelect') ? document.getElementById('agentSelect').value : 'AI';

				var loadingHtml = '<div class="message assistant" id="' + loadingId + '">' +
				'<div class="message-bubble agent-run-card">' +
				'<div class="log-header">' +
				'<span class="log-title">' + escapeHtml(agentName) + '</span>' +
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



				// Register for SSE updates

				if (typeof registerPendingTask === 'function') {
					registerPendingTask(t.id, loadingId, sessionId);
				} else {
					window.pendingTasks = window.pendingTasks || {};
					window.pendingTasks[t.id] = loadingId;
				}

				window.pendingTaskTimes = window.pendingTaskTimes || {};

				window.pendingTaskTimes[t.id] = Date.now();
				// Recover existing progress logs & subscribe for future progress.
				// Both restored and live events use the same safe, structured renderer.
				(function(taskId, lid) {
					var logUrl = window.progressLogDownloadUrl ? window.progressLogDownloadUrl(taskId, sessionId) : '/api/task/progress-log/' + encodeURIComponent(taskId);
					fetch(logUrl)
						.then(function(r) { return r.text(); })
						.then(function(log) {
							if (!log || log === 'null\n' || log === 'null' || isUnavailableProgressLog(log)) return;
							var events = null;
							try { events = JSON.parse(log); } catch(e) {}
							var loadingEl = document.getElementById(lid);
							if (!loadingEl) return;
							if (!Array.isArray(events)) {
								var plainLines = log.split('\n').filter(function(line) { return line.trim(); });
								if (plainLines.length === 1 && isUnavailableProgressLog(plainLines[0])) return;
								for (var j = 0; j < plainLines.length; j++) {
									renderProgressEventBubble(loadingEl, { type: 'progress', status: 'running', message: plainLines[j] });
								}
								return;
							}
							for (var k = 0; k < events.length; k++) {
								renderProgressEventBubble(loadingEl, events[k]);
							}
						}).catch(function(e) { console.log('fetch progress log error:', e); });
					// Subscribe immediately; a slow history request must not delay live updates.
					subscribeTask(taskId, lid);
				})(t.id, loadingId);

			}

		} catch(e) {

			console.log('restorePendingTasks error:', e);

		}

	}

	// subscribeTask opens a dedicated SSE for a single task, auto-closes on completion
	function subscribeTask(taskId, loadingId) {
		var clientId = 'task_' + taskId + '_' + Date.now();
		var es = new EventSource('/api/events?task_id=' + encodeURIComponent(taskId) + '&client_id=' + clientId);

		es.addEventListener('progress', function(e) {
			window._lastProgress = Date.now();
			var hints = document.querySelectorAll('.loading-refresh-hint');
			hints.forEach(function(h) { h.style.display = 'none'; });

			var data = JSON.parse(e.data);
			var loadingEl = loadingId ? document.getElementById(loadingId) : null;
			if (!loadingEl) return;

			// A heartbeat is liveness, not a new workflow stage. Keep the compact
			// status fresh without adding another bubble to the visible timeline.
			if (data.type === "heartbeat") {
				var msg = data.message || "";
				if (typeof filterDebugMsg === "function") msg = filterDebugMsg(msg);
				msg = stripEmbeddedTaskClock(msg);
				msg = userFacingProgressMessage(msg, data.type, data.status);
				var heartbeatText = loadingEl.querySelector('.loading-text');
				if (heartbeatText && msg) {
					heartbeatText.textContent = truncateProgressText(msg, progressStatusTextLimit);
				}
				return;
			}
			renderProgressEventBubble(loadingEl, data);
		});

		var closeAndCleanup = function() {
			es.close();
			if (loadingId) {
				var el = document.getElementById(loadingId);
				if (el && el.parentNode) el.remove();
			}
			var taskSessionId = (typeof getTaskSession === 'function') ? getTaskSession(taskId) : '';
			if (typeof clearPendingTask === 'function') taskSessionId = clearPendingTask(taskId) || taskSessionId;
			else if (window.pendingTasks) delete window.pendingTasks[taskId];
			if (typeof updateTaskLoadingState === 'function') updateTaskLoadingState();
			if (typeof loadSession === 'function' && currentSessionId && (!taskSessionId || currentSessionId === taskSessionId)) {
				try { loadSession(currentSessionId); } catch(e) {}
			} else if (typeof loadSessions === 'function') {
				try { loadSessions(); } catch(e) {}
			}
		};

		es.addEventListener('task_update', function(e) {
			var data = JSON.parse(e.data);
			if (data.status === 'completed' || data.status === 'degraded' || data.status === 'failed' || data.status === 'stopped' || data.status === 'paused') {
				if (data.status === 'failed' || data.status === 'stopped') {
					es.close();
					window.renderFailedTaskResult(data, loadingId);
					return;
				}
				if (data.status === 'paused') {
					closeAndCleanup();
					return;
				}
				if (data.status === 'degraded' && typeof addLog === 'function') {
					addLog('warning', '任务 ' + data.task_id + ' 降级完成（结果可能不完整）');
				}
				window._lastDuration = (data.duration_sec || 0).toFixed(1) + 's';
				closeAndCleanup();
			}
		});

		es.onerror = function() {
			es.close();
			// 重试一次
			setTimeout(function() {
				if (window.pendingTasks && window.pendingTasks[taskId]) {
					subscribeTask(taskId, loadingId);
				}
			}, 3000);
		};
	}

// checkForUpdate polls /api/update/check and shows a banner if update is available
function checkForUpdate() {
	fetch('/api/update/check')
		.then(function(r) { return r.json(); })
		.then(function(data) {
			// Update current version badge
			var verBadge = document.getElementById('currentVersionBadge');
			if (verBadge && data.current) verBadge.textContent = 'v' + data.current;

			if (data.update_available) {
				var banner = document.getElementById('updateBanner');
				if (!banner) return;
				banner.style.display = 'block';
				document.getElementById('updateVersion').textContent = data.version;
				var cvEl = document.getElementById('updateCurrentVer');
				if (cvEl && data.current) cvEl.textContent = 'v' + data.current;
				if (data.download_url) {
					document.getElementById('updateDownloadLink').href = data.download_url;
				}
				if (data.changelog) {
					var short = data.changelog.length > 80 ? data.changelog.substring(0, 80) + '...' : data.changelog;
					document.getElementById('updateChangelog').textContent = ' - ' + short;
				}
			}
		})
		.catch(function(e) { console.log('Update check failed:', e); });
}

// applyUpdate triggers one-click download and auto-update with progress
function applyUpdate() {
	var btn = document.getElementById('updateApplyBtn');
	if (!btn) return;
	btn.disabled = true;
	btn.textContent = '⏳ 0%';

	fetch('/api/update/apply', { method: 'POST' })
		.then(function(r) { return r.json(); })
		.then(function(data) {
			if (!data.ok) {
				btn.textContent = '❌ ' + (data.error || '更新失败');
				btn.disabled = false;
				setTimeout(function() { btn.textContent = '⬇ 一键更新'; }, 3000);
				return;
			}
			// Start polling progress if task_id available
			if (data.task_id) {
				var poll = setInterval(function() {
					fetch('/api/update/progress?task=' + data.task_id)
						.then(function(r) { return r.json(); })
						.then(function(p) {
							if (p.done) {
								clearInterval(poll);
								if (p.error) {
									btn.textContent = '❌ ' + p.error.substring(0, 30);
									btn.disabled = false;
								} else {
									btn.textContent = '✅ 即将重启';
									setTimeout(function() { location.reload(); }, 3000);
								}
							} else {
								btn.textContent = '⏳ ' + (p.percent || 0) + '%';
							}
						});
				}, 500);
			} else if (data.updated) {
				btn.textContent = '✅ 即将重启';
				setTimeout(function() { location.reload(); }, 3000);
			}
		})
		.catch(function(e) {
			btn.textContent = '❌ 网络错误';
			btn.disabled = false;
			setTimeout(function() { btn.textContent = '⬇ 一键更新'; }, 3000);
		});
}
