// TODO module - task management
	function openTodoPanel() {

        document.getElementById('todoPanel').classList.add('open');

        switchTodoTab('list');

		// 点击面板外区域自动关闭
		setTimeout(function() {
			document.addEventListener('click', _todoOutsideClick);
		}, 0);
    }

    function closeTodoPanel() {
        document.getElementById('todoPanel').classList.remove('open');
		document.removeEventListener('click', _todoOutsideClick);
    }

	function _todoOutsideClick(e) {
		var panel = document.getElementById('todoPanel');
		if (panel && !panel.contains(e.target) && !e.target.closest('.hb-btn')) {
			closeTodoPanel();
		}
	}

	    // Cache the original about tab HTML so it survives innerHTML replacements
	    var _aboutTabHTML = document.getElementById('todoTab-about') ? document.getElementById('todoTab-about').innerHTML : '';

	    function switchTodoTab(tab) {

	        var tabs = document.querySelectorAll('.todo-tab');
	        tabs.forEach(function(t) { t.classList.remove('active'); });

	        var content = document.getElementById('todoContent');
	        if (!content) return;

	        if (tab === 'about') {
	            tabs[0].classList.add('active');
	            content.innerHTML = '<div style="padding:16px;font-size:calc(14.3px * var(--macs-font-scale, 1));line-height:1.8;color:#444;">' + (_aboutTabHTML || '<p>加载中...</p>') + '</div>';
	        } else if (tab === 'create') {
	            tabs[1].classList.add('active');
	            content.innerHTML = '';
	            showCreateTodoForm();
	        } else if (tab === 'list') {
	            tabs[2].classList.add('active');
	            content.innerHTML = '<div style="text-align:center;padding:20px;">加载中...</div>';
	            showTodoList();
	        }

	    }


    async function showCreateTodoForm() {
		// 获取可用的任务流列表
		var pipelines = [];
		try { var r = await fetch('/api/pipeline/list'); var d = await r.json(); pipelines = d.pipelines || []; } catch(e) {}

		var pipeOpts = '<option value="">不使用工作流（直接调Agent）</option>';
		for (var i = 0; i < pipelines.length; i++) {
			pipeOpts += '<option value="' + pipelines[i].name + '">' + pipelines[i].name + (pipelines[i].desc ? ' — ' + pipelines[i].desc.substring(0,30) : '') + '</option>';
		}

		var formHtml =
			'<div class="todo-form" style="max-width:500px;margin:0 auto;">' +
			// 标题
			'<div style="margin-bottom:12px;">' +
			'<label style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;">任务标题 <span style="color:#dc3545;">*</span></label>' +
			'<input id="todoTitle" placeholder="如：每日财经早报" style="width:100%;padding:8px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(14.3px * var(--macs-font-scale, 1));">' +
			'</div>' +
			// 描述
			'<div style="margin-bottom:12px;">' +
			'<label style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;">描述 / 输入数据</label>' +
			'<textarea id="todoDescription" rows="2" placeholder="任务描述或输入给AI的指令..." style="width:100%;padding:8px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(14.3px * var(--macs-font-scale, 1));resize:vertical;"></textarea>' +
			'</div>' +
			// Pipeline 选择（核心）
			'<div style="margin-bottom:12px;background:#f0f4ff;border:1px solid #c7d2fe;border-radius:8px;padding:12px;">' +
			'<label style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#4338ca;font-weight:600;">🔀 触发工作流（可选）</label>' +
			'<select id="todoPipeline" style="width:100%;padding:8px;border:1px solid #c7d2fe;border-radius:6px;font-size:calc(14.3px * var(--macs-font-scale, 1));margin-top:4px;" onchange="var s=document.getElementById(\'scheduleRow\');s.style.display=this.value?\'block\':\'none\'">' +
			pipeOpts + '</select>' +
			'<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6366f1;margin-top:4px;">选择工作流后，任务将按流程执行（含审批步骤会自动暂停等待）</div>' +
			'</div>' +
			// 定时（选了Pipeline才显示）
			'<div id="scheduleRow" style="display:none;margin-bottom:12px;">' +
			'<label style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;">⏰ 定时执行（cron）</label>' +
			'<input id="todoSchedule" placeholder="30 8 * * 1-5  (工作日早8:30)" style="width:100%;padding:8px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));">' +
			'<div style="font-size:calc(11px * var(--macs-font-scale, 1));color:#94a3b8;margin-top:2px;">分 时 日 月 周 ｜ 留空=手动触发</div>' +
			'</div>' +
			// 保存
			'<button onclick="submitTodo()" style="width:100%;padding:10px;background:#6366f1;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(15.4px * var(--macs-font-scale, 1));font-weight:600;">创建任务</button>' +
			'</div>';

			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📅 选择日期（1-31）</label>' +

			'<input type=\"number\" id=\"monthlyDay\" min=\"1\" max=\"31\" value=\"1\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +

			'<label style=\"display:block; margin-top:10px; margin-bottom:5px; font-weight:500;\">🕐 执行时间</label>' +

			'<input type=\"time\" id=\"monthlyTime\" value=\"09:00\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +

			'<div style=\"font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-top:5px;\">注意：如果选择31号，但当月没有31号，将在当月最后一天执行</div>' +

			'</div>' +

			// 间隔执行配置

			'<div id=\"intervalConfig\" style=\"display:none;\">' +

			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">⏱️ 间隔时间</label>' +

			'<div style=\"display:flex; gap:10px;\">' +

			'<input type=\"number\" id=\"intervalValue\" min=\"1\" value=\"30\" style=\"flex:2; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +

			'<select id=\"intervalUnit\" style=\"flex:1; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +

			'<option value=\"minute\">分钟</option>' +

			'<option value=\"hour\">小时</option>' +

			'<option value=\"day\">天</option>' +

			'</select>' +

			'</div>' +

			'<div style=\"font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-top:5px;\">⚠️ 建议间隔不要小于10分钟，避免任务执行过于频繁</div>' +

			'</div>' +

			'</div>' +

			// 输入数据说明

			'<div style=\"margin-bottom:15px;\">' +

			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📤 传递给AI的输入数据</label>' +

			'<textarea id=\"todoInputData\" rows=\"4\" placeholder=\"例如:&#10;请帮我总结今天的财经新闻，包括：&#10;1. A股市场表现&#10;2. 重要政策动态&#10;3. 热点板块分析\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px; resize:vertical;\"></textarea>' +

			'<div style=\"font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-top:5px;\">💡 这是你希望AI执行的具体任务描述，描述越清晰，AI执行效果越好</div>' +

			'</div>' +

			// 重试次数

			'<div style=\"margin-bottom:15px;\">' +

			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">🔄 失败重试次数</label>' +

			'<input type=\"number\" id=\"todoMaxRetries\" min=\"0\" max=\"10\" value=\"3\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +

			'<div style=\"font-size:calc(13.2px * var(--macs-font-scale, 1)); color:#6c757d; margin-top:5px;\">任务执行失败时的重试次数，建议3次</div>' +

			'</div>' +

			// 创建按钮

			'<button type=\"button\" onclick=\"createTodo()\" style=\"width:100%; background: linear-gradient(135deg, #28a745 0%, #20c997 100%); color:white; padding:12px; border:none; border-radius:6px; cursor:pointer; font-size:calc(17.6px * var(--macs-font-scale, 1)); font-weight:500;\">✅ 创建定时任务</button>' +

			'</div>';

		document.getElementById('todoContent').innerHTML = formHtml;

		// 初始化调度UI

		updateScheduleUI();

	}





	// 转换用户友好的配置为Cron表达式

	function convertToCronExpression(scheduleType) {

		if (scheduleType === 'daily') {

			var dailyTime = document.getElementById('dailyTime').value;

			if (!dailyTime) dailyTime = '09:00';

			var parts = dailyTime.split(':');

			var hour = parseInt(parts[0]);

			var minute = parseInt(parts[1]);

			// Cron格式: 秒 分 时 日 月 周

			return '0 ' + minute + ' ' + hour + ' * * *';

		}

		else if (scheduleType === 'weekly') {

			var weeklyDay = document.getElementById('weeklyDay').value;

			var weeklyTime = document.getElementById('weeklyTime').value;

			if (!weeklyTime) weeklyTime = '09:00';

			var parts = weeklyTime.split(':');

			var hour = parseInt(parts[0]);

			var minute = parseInt(parts[1]);

			return '0 ' + minute + ' ' + hour + ' * * ' + weeklyDay;

		}

		else if (scheduleType === 'monthly') {

			var monthlyDay = parseInt(document.getElementById('monthlyDay').value);

			var monthlyTime = document.getElementById('monthlyTime').value;

			if (!monthlyTime) monthlyTime = '09:00';

			var parts = monthlyTime.split(':');

			var hour = parseInt(parts[0]);

			var minute = parseInt(parts[1]);

			return '0 ' + minute + ' ' + hour + ' ' + monthlyDay + ' * *';

		}

		return '';

	}



	// 转换间隔配置为秒数

	function convertToIntervalSeconds() {

		var intervalValue = parseInt(document.getElementById('intervalValue').value);

		var intervalUnit = document.getElementById('intervalUnit').value;

		// 最小值限制：至少10分钟

		var minSeconds = 10 * 60; // 10分钟

		var seconds = 0;

		if (intervalUnit === 'minute') {

			seconds = intervalValue * 60;

		} else if (intervalUnit === 'hour') {

			seconds = intervalValue * 60 * 60;

		} else if (intervalUnit === 'day') {

			seconds = intervalValue * 24 * 60 * 60;

		}

		// 如果小于10分钟，给出警告并自动调整为10分钟

		if (seconds < minSeconds && seconds > 0) {

			alert('⚠️ 间隔时间不能小于10分钟，已自动调整为10分钟');

			return minSeconds;

		}

		return seconds;

	}

    function toggleScheduleOptions() {

		var typeSelect = document.getElementById('todoScheduleType');

		if (!typeSelect) return;

		var type = typeSelect.value;

		var scheduleDiv = document.getElementById('scheduleOptions');

		var cronDiv = document.getElementById('cronOptions');

		var intervalDiv = document.getElementById('intervalOptions');

		var runAtInput = document.getElementById('runAt');

		if (scheduleDiv) scheduleDiv.style.display = 'block';

		if (cronDiv) cronDiv.style.display = 'none';

		if (intervalDiv) intervalDiv.style.display = 'none';

		if (runAtInput) runAtInput.style.display = 'none';

		if (type === 'cron' && cronDiv) {

			cronDiv.style.display = 'block';

		} else if (type === 'interval' && intervalDiv) {

			intervalDiv.style.display = 'block';

		} else if (type === 'once' && runAtInput) {

			runAtInput.style.display = 'block';

		}

	}



	// createTodo 函数

	async function submitTodo() {
		var title = document.getElementById('todoTitle').value.trim();
		if (!title) { alert('请输入任务标题'); return; }

		var pipeline = document.getElementById('todoPipeline').value;
		var schedule = document.getElementById('todoSchedule').value.trim();

		var body = JSON.stringify({
			title: title,
			description: document.getElementById('todoDescription').value.trim(),
			pipeline_name: pipeline,
			cron_expr: schedule,
			priority: 'normal'
		});

		try {
			var resp = await fetch('/api/todo/create', {
				method: 'POST', headers: {'Content-Type': 'application/json'}, body: body
			});
			var data = await resp.json();
			if (data.ok || data.todo) {
				var msg = '任务已创建: ' + title;
				if (pipeline) msg += ' (工作流: ' + pipeline + ')';
				if (schedule) msg += ' [定时: ' + schedule + ']';
				alert(msg);
				switchTodoTab('list');
			} else {
				alert('创建失败: ' + (data.error || ''));
			}
		} catch(e) { alert('创建失败: ' + e.message); }
	}

	async function createTodo() {

		// 获取表单值

		var titleEl = document.getElementById('todoTitle');

		if (!titleEl || !titleEl.value) {

			alert('请输入任务标题');

			return;

		}

		var title = titleEl.value;

		var description = document.getElementById('todoDescription') ? document.getElementById('todoDescription').value : '';

		var priority = document.getElementById('todoPriority') ? document.getElementById('todoPriority').value : 'normal';

		var agentName = document.getElementById('todoAgent') ? document.getElementById('todoAgent').value : '';

		var inputData = document.getElementById('todoInputData') ? document.getElementById('todoInputData').value : '';

		var maxRetries = document.getElementById('todoMaxRetries') ? parseInt(document.getElementById('todoMaxRetries').value) : 3;

		// 获取调度类型

		var radios = document.getElementsByName('scheduleTypeRadio');

		var scheduleType = '';

		for (var i = 0; i < radios.length; i++) {

			if (radios[i].checked) {

				scheduleType = radios[i].value;

				break;

			}

		}

		var data = {

			title: title,

			description: description,

			priority: priority,

			agent_name: agentName,

			input_data: inputData,

			max_retries: maxRetries

		};

		// 根据调度类型构建配置

		if (scheduleType === 'once') {

			data.schedule_type = 'once';

			var runAt = document.getElementById('runAt') ? document.getElementById('runAt').value : '';

			if (runAt) {

				data.run_at = runAt;

			}

		} 

		else if (scheduleType === 'daily' || scheduleType === 'weekly' || scheduleType === 'monthly') {

			data.schedule_type = 'cron';

			data.cron_expr = convertToCronExpression(scheduleType);

			// 添加友好的描述

			var scheduleDesc = '';

			if (scheduleType === 'daily') {

				var dailyTime = document.getElementById('dailyTime').value || '09:00';

				scheduleDesc = '每天 ' + dailyTime + ' 执行';

			} else if (scheduleType === 'weekly') {

				var weekdays = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

				var weeklyDay = parseInt(document.getElementById('weeklyDay').value);

				var weeklyTime = document.getElementById('weeklyTime').value || '09:00';

				scheduleDesc = '每周' + weekdays[weeklyDay] + ' ' + weeklyTime + ' 执行';

			} else if (scheduleType === 'monthly') {

				var monthlyDay = document.getElementById('monthlyDay').value;

				var monthlyTime = document.getElementById('monthlyTime').value || '09:00';

				scheduleDesc = '每月' + monthlyDay + '号 ' + monthlyTime + ' 执行';

			}

			console.log('定时任务配置:', scheduleDesc, 'Cron:', data.cron_expr);

		} 

		else if (scheduleType === 'interval') {

			data.schedule_type = 'interval';

			data.interval_sec = convertToIntervalSeconds();

			var intervalValue = document.getElementById('intervalValue').value;

			var intervalUnit = document.getElementById('intervalUnit').value;

			var unitText = {'minute':'分钟', 'hour':'小时', 'day':'天'};

			console.log('间隔任务配置: 每', intervalValue, unitText[intervalUnit], '执行一次');

		}

		try {

			var response = await fetch('/api/todo/create', {

				method: 'POST',

				headers: { 'Content-Type': 'application/json' },

				body: JSON.stringify(data)

			});

			if (response.ok) {

				var result = await response.json();

				alert('✅ 定时任务创建成功！\n任务ID: ' + (result.todo_id || '已创建'));

				switchTodoTab('list');

			} else {

				var err = await response.text();

				alert('创建失败: ' + err);

			}

		} catch(err) {

			alert('创建失败: ' + err.message);

		}

	}



	async function showTodoList() {

		try {


				var resp = await fetch('/api/todo/list?limit=50');

				if (!resp.ok) {
					var errText = await resp.text();
					document.getElementById('todoContent').innerHTML = '<div style="color:#dc3545;text-align:center;padding:20px;">请求失败: ' + resp.status + '</div>';
					return;
				}

				var data = await resp.json();

				var todos = data.todos || [];

			var statusText = { pending: '待执行', running: '执行中', completed: '已完成', failed: '失败', cancelled: '已取消' };
				var statusIcon = { pending: 'clock', running: 'loader', completed: 'check', failed: 'x-circle', cancelled: 'x-circle' };

			var html = '';

			for (var i = 0; i < todos.length; i++) {

				var t = todos[i];

				var typeDesc = { once: '一次性', cron: '定时', interval: '间隔' };
				var schedInfo = typeDesc[t.schedule_type] || t.schedule_type || 'once';
				if (t.cron_expr) schedInfo += ' (' + escapeHtml(t.cron_expr) + ')';
				if (t.interval_sec) schedInfo += ' (' + t.interval_sec + 's)';

				html += '<div class="todo-item ' + t.status + '">' +

					'<div class="todo-title">' + escapeHtml(t.title) + '</div>' +

					'<div class="todo-meta"><span class="ico" data-ico="' + (statusIcon[t.status] || 'help') + '"></span> ' + (statusText[t.status] || t.status) + ' | ' + schedInfo + ' | 执行: ' + (t.run_count || 0) + '次';

				html += '</div>';

				if (t.status === 'failed' && t.last_error) {
					html += '<div style="color:#dc3545;font-size:calc(12.1px * var(--macs-font-scale, 1));margin-top:4px;padding:4px 8px;background:#fff5f5;border-radius:4px;word-break:break-all;">失败原因: ' + escapeHtml(t.last_error.substring(0, 200)) + '</div>';
				}

				if (t.last_run_at) {
					html += '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#888;margin-top:2px;">上次: ' + formatDate(t.last_run_at) + '</div>';
				}

				if (t.next_run_at && t.next_run_at.indexOf('0001') !== 0 && (t.status === 'pending' || t.status === 'running')) {
					html += '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#1976D2;">下次: ' + formatDate(t.next_run_at) + '</div>';
				}

				html += '<div style="margin-top:6px;display:flex;gap:4px;">' +
					'<button onclick="editTodo(\'' + t.todo_id + '\')" style="background:#1976D2;color:white;border:none;border-radius:4px;padding:4px 8px;font-size:calc(13.2px * var(--macs-font-scale, 1));">编辑</button>' +
					'<button onclick="showTodoHistory(\'' + t.todo_id + '\')" style="background:#6c757d;color:white;border:none;border-radius:4px;padding:4px 8px;font-size:calc(13.2px * var(--macs-font-scale, 1));">历史</button>' +
					'<button onclick="cancelTodo(\'' + t.todo_id + '\')" style="background:#dc3545;color:white;border:none;border-radius:4px;padding:4px 8px;font-size:calc(13.2px * var(--macs-font-scale, 1));">删除</button>' +
					'</div>' +
					'</div>';

			}

			document.getElementById('todoContent').innerHTML = html;
			setTimeout(function() {
				var icos = document.querySelectorAll('#todoContent .ico[data-ico]');
				for (var j = 0; j < icos.length; j++) {
					var nm = icos[j].getAttribute('data-ico');
					if (window.I && window.I[nm]) icos[j].innerHTML = window.I[nm];
				}
			}, 10);

			if (todos.length === 0) {

				document.getElementById('todoContent').innerHTML = '<div style="text-align:center;padding:40px;color:#888;">暂无TODO任务<br><br><small>可通过对话让 AI 创建，如 "每天8点汇总新闻"</small></div>';

			}

		} catch(err) { 

			document.getElementById('todoContent').innerHTML = '<div style="color:#dc3545;text-align:center;padding:20px;">加载失败: ' + (err.message || '未知错误') + '</div>'; 

		}

	}

	    // Helper: parse cron expr back to friendly UI values
		function parseCronToUI(cronExpr, intervalSec) {
			var result = { scheduleType: 'once', dailyTime: '09:00', weeklyDay: '1', weeklyTime: '09:00', monthlyDay: '1', monthlyTime: '09:00', intervalValue: 30, intervalUnit: 'minute' };
			if (!cronExpr && !intervalSec) return result;
			if (intervalSec && intervalSec > 0) {
				result.scheduleType = 'interval';
				if (intervalSec >= 86400) { result.intervalValue = Math.floor(intervalSec / 86400); result.intervalUnit = 'day'; }
				else if (intervalSec >= 3600) { result.intervalValue = Math.floor(intervalSec / 3600); result.intervalUnit = 'hour'; }
				else if (intervalSec >= 60) { result.intervalValue = Math.floor(intervalSec / 60); result.intervalUnit = 'minute'; }
				else { result.intervalValue = intervalSec; result.intervalUnit = 'minute'; }
				return result;
			}
			if (!cronExpr) return result;
			var parts = cronExpr.trim().split(/\s+/);
			if (parts.length < 5) return result;
			var minute = parts[1], hour = parts[2], dom = parts[3], dow = parts[4];
			var timeStr = hour.padStart(2,'0') + ':' + minute.padStart(2,'0');
			if (dow !== '*' && dom === '*') {
				result.scheduleType = 'weekly';
				result.weeklyTime = timeStr;
				result.weeklyDay = dow;
			} else if (dom !== '*' && dow === '*') {
				result.scheduleType = 'monthly';
				result.monthlyTime = timeStr;
				result.monthlyDay = dom;
			} else {
				result.scheduleType = 'daily';
				result.dailyTime = timeStr;
			}
			return result;
		}

	    async function editTodo(todoId) {

			var existing = document.getElementById('todoEditModal');
			if (existing) existing.remove();

			try {

				var resp = await fetch('/api/todo/' + todoId);
				var todo = await resp.json();
				var ui = parseCronToUI(todo.cron_expr, todo.interval_sec);

				var agentNames = [];
				try {
					var ar = await fetch('/api/agents/names');
					var ad = await ar.json();
					agentNames = ad.agents || ad.names || [];
				} catch(e) {}
				var agentOptions = agentNames.map(function(n) {
					var sel = n === todo.agent_name ? ' selected' : '';
					return '<option value="' + escapeHtml(n) + '"' + sel + '>' + escapeHtml(n) + '</option>';
				}).join('');

				var checked = {};
				checked[ui.scheduleType] = ' checked';
				var checkOnce = checked.once || '';
				var checkDaily = checked.daily || '';
				var checkWeekly = checked.weekly || '';
				var checkMonthly = checked.monthly || '';
				var checkInterval = checked.interval || '';

				var showOnce = ui.scheduleType === 'once' ? '' : 'display:none;';
				var showDaily = ui.scheduleType === 'daily' ? '' : 'display:none;';
				var showWeekly = ui.scheduleType === 'weekly' ? '' : 'display:none;';
				var showMonthly = ui.scheduleType === 'monthly' ? '' : 'display:none;';
				var showInterval = ui.scheduleType === 'interval' ? '' : 'display:none;';

				var selSun = ui.weeklyDay === '0' ? ' selected' : '';
				var selMon = ui.weeklyDay === '1' ? ' selected' : '';
				var selTue = ui.weeklyDay === '2' ? ' selected' : '';
				var selWed = ui.weeklyDay === '3' ? ' selected' : '';
				var selThu = ui.weeklyDay === '4' ? ' selected' : '';
				var selFri = ui.weeklyDay === '5' ? ' selected' : '';
				var selSat = ui.weeklyDay === '6' ? ' selected' : '';
				var selMin = ui.intervalUnit === 'minute' ? ' selected' : '';
				var selHour = ui.intervalUnit === 'hour' ? ' selected' : '';
				var selDay = ui.intervalUnit === 'day' ? ' selected' : '';

				var overlay = document.createElement('div');
				overlay.id = 'todoEditModal';
				overlay.style.cssText = 'position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,0.5);z-index:9999;display:flex;align-items:center;justify-content:center;';
				overlay.onclick = function(e) { if (e.target === overlay) overlay.remove(); };

				var modal = document.createElement('div');
				modal.style.cssText = 'background:white;border-radius:12px;padding:24px;width:460px;max-width:92vw;max-height:88vh;overflow-y:auto;box-shadow:0 8px 32px rgba(0,0,0,0.3);';
				modal.onclick = function(e) { e.stopPropagation(); };

modal.innerHTML = '<h3 style="margin:0 0 16px;color:#1976D2;">编辑定时任务</h3>' +
'<label style="display:block;margin-bottom:4px;font-weight:500;font-size:calc(14.3px * var(--macs-font-scale, 1));">任务标题</label>' +
'<input id="editTitle_' + todoId + '" value="' + escapeHtml(todo.title || '') + '" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;margin-bottom:12px;box-sizing:border-box;">' +
'<label style="display:block;margin-bottom:4px;font-weight:500;font-size:calc(14.3px * var(--macs-font-scale, 1));">任务描述</label>' +
'<textarea id="editDesc_' + todoId + '" rows="2" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;margin-bottom:12px;resize:vertical;box-sizing:border-box;">' + escapeHtml(todo.description || '') + '</textarea>' +
'<label style="display:block;margin-bottom:4px;font-weight:500;font-size:calc(14.3px * var(--macs-font-scale, 1));">输入数据</label>' +
'<textarea id="editInput_' + todoId + '" rows="2" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;margin-bottom:12px;resize:vertical;box-sizing:border-box;">' + escapeHtml(todo.input_data || '') + '</textarea>' +
'<label style="display:block;margin-bottom:4px;font-weight:500;font-size:calc(14.3px * var(--macs-font-scale, 1));">指定Agent</label>' +
'<select id="editAgent_' + todoId + '" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;margin-bottom:12px;box-sizing:border-box;"><option value="">自动选择</option>' + agentOptions + '</select>' +
'<label style="display:block;margin-bottom:4px;font-weight:500;font-size:calc(14.3px * var(--macs-font-scale, 1));">调度类型</label>' +
'<div style="display:flex;gap:6px;flex-wrap:wrap;margin-bottom:12px;">' +
'<label style="display:flex;align-items:center;gap:3px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));"><input type="radio" name="editScheduleRadio" value="once"' + checkOnce + ' onchange="window._editUpdateUI()"> 一次性</label>' +
'<label style="display:flex;align-items:center;gap:3px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));"><input type="radio" name="editScheduleRadio" value="daily"' + checkDaily + ' onchange="window._editUpdateUI()"> 每天</label>' +
'<label style="display:flex;align-items:center;gap:3px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));"><input type="radio" name="editScheduleRadio" value="weekly"' + checkWeekly + ' onchange="window._editUpdateUI()"> 每周</label>' +
'<label style="display:flex;align-items:center;gap:3px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));"><input type="radio" name="editScheduleRadio" value="monthly"' + checkMonthly + ' onchange="window._editUpdateUI()"> 每月</label>' +
'<label style="display:flex;align-items:center;gap:3px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));"><input type="radio" name="editScheduleRadio" value="interval"' + checkInterval + ' onchange="window._editUpdateUI()"> 间隔</label>' +
'</div>' +
'<div id="editConfigArea" style="margin-bottom:12px;padding:12px;background:#f8f9fa;border-radius:8px;">' +
'<div id="editOnceConfig" style="' + showOnce + '"><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">' + (todo.status === 'completed' || todo.status === 'failed' ? '已执行时间' : '执行时间 (不填则立即执行)') + '</label><input type="datetime-local" id="editRunAt" value="' + ((function() { var t = (todo.run_at && todo.run_at.indexOf('0001') !== 0) ? todo.run_at : (todo.last_run_at || ''); return (t && t.indexOf('0001') !== 0) ? t.substring(0, 16) : ''; })()) + '" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;box-sizing:border-box;margin-top:4px;"></div>' +
'<div id="editDailyConfig" style="' + showDaily + '"><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">每天执行时间</label><input type="time" id="editDailyTime" value="' + ui.dailyTime + '" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;box-sizing:border-box;margin-top:4px;"></div>' +
'<div id="editWeeklyConfig" style="' + showWeekly + '"><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">星期</label><select id="editWeeklyDay" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;box-sizing:border-box;margin-top:4px;"><option value="0"' + selSun + '>周日</option><option value="1"' + selMon + '>周一</option><option value="2"' + selTue + '>周二</option><option value="3"' + selWed + '>周三</option><option value="4"' + selThu + '>周四</option><option value="5"' + selFri + '>周五</option><option value="6"' + selSat + '>周六</option></select><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;margin-top:6px;display:block;">时间</label><input type="time" id="editWeeklyTime" value="' + ui.weeklyTime + '" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;box-sizing:border-box;margin-top:4px;"></div>' +
'<div id="editMonthlyConfig" style="' + showMonthly + '"><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">日期 (1-31)</label><input type="number" id="editMonthlyDay" min="1" max="31" value="' + ui.monthlyDay + '" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;box-sizing:border-box;margin-top:4px;"><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;margin-top:6px;display:block;">时间</label><input type="time" id="editMonthlyTime" value="' + ui.monthlyTime + '" style="width:100%;padding:8px;border:1px solid #ddd;border-radius:6px;box-sizing:border-box;margin-top:4px;"></div>' +
'<div id="editIntervalConfig" style="' + showInterval + '"><label style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">间隔时间</label><div style="display:flex;gap:8px;margin-top:4px;"><input type="number" id="editIntervalValue" min="1" value="' + ui.intervalValue + '" style="flex:2;padding:8px;border:1px solid #ddd;border-radius:6px;"><select id="editIntervalUnit" style="flex:1;padding:8px;border:1px solid #ddd;border-radius:6px;"><option value="minute"' + selMin + '>分钟</option><option value="hour"' + selHour + '>小时</option><option value="day"' + selDay + '>天</option></select></div></div>' +
'</div>' +
'<div style="display:flex;gap:8px;justify-content:flex-end;">' +
'<button onclick="document.getElementById(\'todoEditModal\').remove()" style="padding:8px 20px;border:1px solid #ddd;border-radius:6px;background:#f5f5f5;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">取消</button>' +
'<button onclick="saveTodoEdit(\'' + todoId + '\')" style="padding:8px 20px;border:none;border-radius:6px;background:#1976D2;color:white;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">保存修改</button>';


				overlay.appendChild(modal);
				document.body.appendChild(overlay);

				window._editUpdateUI = function() {
					var radios = document.getElementsByName('editScheduleRadio');
					var val = 'once';
					for (var i = 0; i < radios.length; i++) { if (radios[i].checked) { val = radios[i].value; break; } }
					var ids = ['editOnceConfig','editDailyConfig','editWeeklyConfig','editMonthlyConfig','editIntervalConfig'];
					var types = ['once','daily','weekly','monthly','interval'];
					for (var j = 0; j < ids.length; j++) {
						var el = document.getElementById(ids[j]);
						if (el) el.style.display = types[j] === val ? 'block' : 'none';
					}
				};

			} catch (err) {
				alert('加载失败: ' + err.message);
			}

		}

		async function saveTodoEdit(todoId) {

			var updates = {};
			var titleEl = document.getElementById('editTitle_' + todoId);
			if (titleEl) updates.title = titleEl.value;
			var descEl = document.getElementById('editDesc_' + todoId);
			if (descEl) updates.description = descEl.value;
			var inputEl = document.getElementById('editInput_' + todoId);
			if (inputEl) updates.input_data = inputEl.value;
			var agentEl = document.getElementById('editAgent_' + todoId);
			if (agentEl) updates.agent_name = agentEl.value;

			var radios = document.getElementsByName('editScheduleRadio');
			var scheduleType = 'once';
			for (var i = 0; i < radios.length; i++) { if (radios[i].checked) { scheduleType = radios[i].value; break; } }

			if (scheduleType === 'once') {
				updates.schedule_type = 'once';
				updates.cron_expr = '';
				updates.interval_sec = 0;
				var runAt = document.getElementById('editRunAt');
				if (runAt && runAt.value) updates.run_at = runAt.value + ':00';
			} else if (scheduleType === 'daily') {
				updates.schedule_type = 'cron';
				var dt = document.getElementById('editDailyTime');
				var dailyTime = (dt ? dt.value : '09:00') || '09:00';
				var parts = dailyTime.split(':');
				updates.cron_expr = '0 ' + parseInt(parts[1]) + ' ' + parseInt(parts[0]) + ' * * *';
				updates.interval_sec = 0;
			} else if (scheduleType === 'weekly') {
				updates.schedule_type = 'cron';
				var wdEl = document.getElementById('editWeeklyDay');
				var wd = wdEl ? wdEl.value : '1';
				var wtEl = document.getElementById('editWeeklyTime');
				var wt = (wtEl ? wtEl.value : '09:00') || '09:00';
				var wp = wt.split(':');
				updates.cron_expr = '0 ' + parseInt(wp[1]) + ' ' + parseInt(wp[0]) + ' * * ' + wd;
				updates.interval_sec = 0;
			} else if (scheduleType === 'monthly') {
				updates.schedule_type = 'cron';
				var mdEl = document.getElementById('editMonthlyDay');
				var md = mdEl ? mdEl.value : '1';
				var mtEl = document.getElementById('editMonthlyTime');
				var mt = (mtEl ? mtEl.value : '09:00') || '09:00';
				var mp = mt.split(':');
				updates.cron_expr = '0 ' + parseInt(mp[1]) + ' ' + parseInt(mp[0]) + ' ' + md + ' * *';
				updates.interval_sec = 0;
			} else if (scheduleType === 'interval') {
				updates.schedule_type = 'interval';
				updates.cron_expr = '';
				var ivEl = document.getElementById('editIntervalValue');
				var iv = parseInt(ivEl ? ivEl.value : '30') || 30;
				var iuEl = document.getElementById('editIntervalUnit');
				var iu = iuEl ? iuEl.value : 'minute';
				if (iu === 'day') iv *= 86400;
				else if (iu === 'hour') iv *= 3600;
				else iv *= 60;
				updates.interval_sec = iv;
			}

			try {
				var response = await fetch('/api/todo/' + todoId, {
					method: 'PUT',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify(updates)
				});
				if (response.ok) {
					var modal = document.getElementById('todoEditModal');
					if (modal) modal.remove();
					showTodoList();
				} else {
					var errText = await response.text();
					alert('更新失败: ' + errText);
				}
			} catch (err) {
				alert('更新失败: ' + err.message);
			}

		}
    async function showTodoHistory(todoId) {

        var existing = document.getElementById('todoHistoryModal');
        if (existing) existing.remove();

        var overlay = document.createElement('div');
        overlay.id = 'todoHistoryModal';
        overlay.style.cssText = 'position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,0.5);z-index:9999;display:flex;align-items:center;justify-content:center;';
        overlay.onclick = function(e) { if (e.target === overlay) overlay.remove(); };

        var modal = document.createElement('div');
        modal.style.cssText = 'background:#fff;border-radius:14px;padding:0;width:520px;max-width:94vw;max-height:88vh;overflow:hidden;box-shadow:0 12px 48px rgba(0,0,0,0.25);display:flex;flex-direction:column;';
        modal.onclick = function(e) { e.stopPropagation(); };

        modal.innerHTML = '<div style="display:flex;align-items:center;justify-content:space-between;padding:20px 24px 16px;border-bottom:1px solid #eee;">' +
            '<h3 style="margin:0;font-size:calc(17.6px * var(--macs-font-scale, 1));font-weight:700;color:#1a1a2e;">执行历史</h3>' +
            '<button onclick="document.getElementById(\'todoHistoryModal\').remove()" style="width:28px;height:28px;border:none;border-radius:50%;background:#f0f0f0;color:#666;font-size:calc(17.6px * var(--macs-font-scale, 1));line-height:28px;cursor:pointer;transition:background 0.2s;" onmouseover="this.style.background=\'#e0e0e0\'" onmouseout="this.style.background=\'#f0f0f0\'">×</button>' +
            '</div>' +
            '<div id="historyContent" style="padding:16px 24px 20px;overflow-y:auto;max-height:62vh;text-align:center;color:#888;">加载中...</div>';

        overlay.appendChild(modal);
        document.body.appendChild(overlay);

        try {
            var resp = await fetch('/api/todo/' + todoId + '/logs');
            var data = await resp.json();
            var logs = data.logs || [];
            var content = document.getElementById('historyContent');
            if (!content) return;

            if (logs.length === 0) {
                content.innerHTML = '<div style="text-align:center;color:#aaa;padding:30px 0;font-size:calc(14.3px * var(--macs-font-scale, 1));">暂无执行记录</div>';
                return;
            }

            var h = '<div style="position:relative;padding-left:24px;">';
            h += '<div style="position:absolute;left:2px;top:6px;bottom:6px;width:2px;background:#e8ecf1;border-radius:1px;"></div>';

            for (var i = 0; i < logs.length; i++) {
                var l = logs[i];
                var isLast = i === logs.length - 1;
                var idx = logs.length - i;
                var cfg;
                if (l.status === 'completed') {
                    cfg = {dot:'#10b981',badge:'完成',badgeBg:'#d1fae5',badgeColor:'#059669',border:'#a7f3d0'};
                } else if (l.status === 'failed') {
                    cfg = {dot:'#ef4444',badge:'失败',badgeBg:'#fee2e2',badgeColor:'#dc2626',border:'#fecaca'};
                } else {
                    cfg = {dot:'#f59e0b',badge:'执行中',badgeBg:'#fef3c7',badgeColor:'#d97706',border:'#fde68a'};
                }
                var durStr = l.duration_ms ? formatDuration(l.duration_ms) : '-';
                var timeStr = l.run_at ? formatDate(l.run_at) : '';

                h += '<div style="position:relative;margin-bottom:' + (isLast ? '0' : '14px') + ';">';
                h += '<div style="position:absolute;left:-24px;top:7px;width:8px;height:8px;border-radius:50%;background:' + cfg.dot + ';box-shadow:0 0 0 4px ' + cfg.dot + '22;z-index:1;"></div>';

                h += '<div style="border:1px solid ' + cfg.border + ';border-radius:10px;padding:12px 14px;background:#fafbfc;transition:box-shadow 0.15s;" onmouseover="this.style.boxShadow=\'0 2px 8px rgba(0,0,0,0.06)\'" onmouseout="this.style.boxShadow=\'none\'">';

                h += '<div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;">';
                h += '<div style="display:flex;align-items:center;gap:8px;">';
                h += '<span style="font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:600;color:#8b8fa3;">#' + idx + '</span>';
                h += '<span style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">' + escapeHtml(timeStr) + '</span>';
                h += '</div>';
                h += '<div style="display:flex;align-items:center;gap:8px;">';
                h += '<span style="font-size:calc(11px * var(--macs-font-scale, 1));padding:2px 8px;border-radius:10px;background:' + cfg.badgeBg + ';color:' + cfg.badgeColor + ';font-weight:600;">' + cfg.badge + '</span>';
                h += '<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;">' + durStr + '</span>';
                h += '</div></div>';

                if (l.error) {
                    var errId = 'herr_' + todoId + '_' + i;
                    h += '<div style="margin-top:4px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#dc2626;background:#fef2f2;border-radius:6px;padding:6px 8px;max-height:20px;overflow:hidden;cursor:pointer;white-space:pre-wrap;word-break:break-all;line-height:1.4;transition:max-height 0.3s;" onclick="var t=this;t.style.maxHeight=t.style.maxHeight===\'none\'?\'20px\':\'none\'" id="' + errId + '">' + escapeHtml(l.error.substring(0, 500)) + '</div>';
                }
                if (l.result) {
                    var resId = 'hres_' + todoId + '_' + i;
                    h += '<div style="margin-top:4px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#374151;background:#f3f4f6;border-radius:6px;padding:6px 8px;max-height:20px;overflow:hidden;cursor:pointer;white-space:pre-wrap;word-break:break-all;line-height:1.4;transition:max-height 0.3s;" onclick="var t=this;t.style.maxHeight=t.style.maxHeight===\'none\'?\'20px\':\'none\'" id="' + resId + '">' + escapeHtml(l.result.substring(0, 500)) + (l.result.length > 500 ? '<span style="color:#6b7280;font-size:calc(11px * var(--macs-font-scale, 1));"> ...点击展开</span>' : '') + '</div>';
                }

                h += '</div></div>';
            }
            h += '</div>';
            content.innerHTML = h;
        } catch (err) {
            var content = document.getElementById('historyContent');
            if (content) content.innerHTML = '<div style="color:#dc3545;text-align:center;padding:20px;">加载失败: ' + (err.message || '') + '</div>';
        }

    }
	    async function cancelTodo(todoId) {

			if (!confirm('确定要删除这个任务吗？')) return;

			try {

				const response = await fetch('/api/todo/' + todoId, { method: 'DELETE' });

				if (response.ok) {

					showTodoList();

				} else {

					alert('删除失败');

				}

			} catch (err) {

				alert('删除失败: ' + err.message);

			}

		}
