    // PDF.js 配置
    pdfjsLib.GlobalWorkerOptions.workerSrc = 'https://cdnjs.cloudflare.com/ajax/libs/pdf.js/2.16.105/pdf.worker.min.js';
    
    let currentSessionId = null;
	let currentUserId = null; // 可选，可以从登录信息获取
    let agents = [];
    let isLoading = false;
    let currentMode = 'single';
    let currentCollabTaskId = null;
    let logs = [];

	let currentFilePath = '/';
	let currentFileList = [];
    let filteredFileList = [];
    let currentSessionSource = 'web';

	const style = document.createElement('style');
    document.head.appendChild(style);
    let currentTaskId = null;
    let currentLoadingId = null;
	let currentConfig = null;

	let currentTaskTab = 'running';
	
	function openTaskPanel() {
		document.getElementById('taskPanel').classList.add('open');
		loadTasks();
	}

	function closeTaskPanel() {
		document.getElementById('taskPanel').classList.remove('open');
	}

	function switchTaskTab(tab) {
		currentTaskTab = tab;
		var tabs = document.querySelectorAll('.task-tab');
		for (var i = 0; i < tabs.length; i++) {
			tabs[i].classList.remove('active');
		}
		if (tab === 'running') {
			tabs[0].classList.add('active');
		} else if (tab === 'pending') {
			tabs[1].classList.add('active');
		} else {
			tabs[2].classList.add('active');
		}
		loadTasks();
	}

	function openHelpModal() {
		document.getElementById('helpModal').classList.add('active');
	}

	function closeHelpModal() {
		document.getElementById('helpModal').classList.remove('active');
	}

	async function loadTasks() {
		try {
			var response = await fetch('/api/tasks/running');
			var data = await response.json();
			
			// 更新统计
			document.getElementById('runningCount').textContent = (data.running ? data.running.length : 0);
			document.getElementById('pendingCount').textContent = (data.pending ? data.pending.length : 0);
			document.getElementById('totalCount').textContent = (data.total || 0);
			
			var tasks = [];
			if (currentTaskTab === 'running') {
				tasks = data.running || [];
			} else if (currentTaskTab === 'pending') {
				tasks = data.pending || [];
			} else {
				tasks = (data.running || []).concat(data.pending || [], data.completed || [], data.stopped || [], data.failed || []);
				// 按创建时间排序
				tasks.sort(function(a, b) {
					return new Date(b.created_at) - new Date(a.created_at);
				});
			}
			
			var container = document.getElementById('taskContent');
			if (tasks.length === 0) {
				container.innerHTML = '<div style="text-align: center; padding: 40px; color: #adb5bd;">暂无任务</div>';
				return;
			}
			
			var html = '';
			for (var i = 0; i < tasks.length; i++) {
				var task = tasks[i];
				var statusTextMap = {
					'running': '🔄 运行中',
					'pending': '⏳ 等待中',
					'completed': '✅ 已完成',
					'stopped': '⏹️ 已停止',
					'failed': '❌ 失败'
				};
				var statusText = statusTextMap[task.status] || task.status;
				
				html += '<div class="task-item ' + task.status + '">' +
					'<div class="task-id">ID: ' + (task.id ? task.id.substring(0, 20) : '未知') + '...</div>' +
					'<div class="task-title">📝 类型: ' + (task.type || 'chat') + '</div>' +
					'<div class="task-message">💬 消息: ' + escapeHtml(task.message || '无') + '</div>' +
					'<div class="task-meta">' +
					'<span>🤖 Agent: ' + (task.agent_name || '默认') + '</span>' +
					'<span>⏱️ 耗时: ' + (task.duration || '计算中...') + '</span>' +
					'</div>' +
					'<div class="task-meta">' +
					'<span>📅 创建: ' + formatDate(task.created_at) + '</span>' +
					'<span>📊 状态: ' + statusText + '</span>' +
					'</div>';
				
				// 只有运行中或等待中的任务才能停止
				if (task.status === 'running' || task.status === 'pending') {
					html += '<div class="task-actions">' +
						'<button class="task-stop-btn" onclick="stopTask(\'' + task.id + '\')">⏹️ 停止任务</button>' +
						'</div>';
				}
				
				if (task.error) {
					html += '<div class="task-meta" style="color: #dc3545;">❌ 错误: ' + escapeHtml(task.error) + '</div>';
				}
				
				html += '</div>';
			}
			
			container.innerHTML = html;
		} catch (err) {
			console.error('加载任务失败:', err);
			document.getElementById('taskContent').innerHTML = '<div style="text-align: center; padding: 40px; color: #dc3545;">加载失败: ' + err.message + '</div>';
		}
	}

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
			if (document.getElementById('taskPanel').classList.contains('open')) {
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
    }

	function openTodoPanel() {
        document.getElementById('todoPanel').classList.add('open');
        switchTodoTab('create');
    }
    
    function closeTodoPanel() {
        document.getElementById('todoPanel').classList.remove('open');
    }
    
    function switchTodoTab(tab) {
        const tabs = document.querySelectorAll('.todo-tab');
        tabs.forEach(t => t.classList.remove('active'));
        if (tab === 'create') {
            tabs[0].classList.add('active');
            showCreateTodoForm();
        } else {
            tabs[1].classList.add('active');
            showTodoList();
        }
    }
    
    async function showCreateTodoForm() {
		let agentNames = [];
		try {
			const resp = await fetch('/api/agents/names');
			const data = await resp.json();
			agentNames = data.agents || [];
		} catch(e) {
			console.error('获取Agent列表失败:', e);
		}
		
		// 构建 agent 选项 HTML
		let agentOptions = '<option value=\"\">🤖 自动选择Agent</option>';
		for (let i = 0; i < agentNames.length; i++) {
			agentOptions += '<option value=\"' + agentNames[i] + '\">' + agentNames[i] + '</option>';
		}
		
		var formHtml = 
			'<div class=\"todo-form\">' +
			
			// 任务标题
			'<div style=\"margin-bottom:15px;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📝 task title <span style=\"color:#dc3545;\">*</span></label>' +
			'<input type=\"text\" id=\"todoTitle\" placeholder=\"例如: 每天总结财经新闻\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\" required>' +
			'</div>' +
			
			// 任务描述
			'<div style=\"margin-bottom:15px;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📋 task description</label>' +
			'<textarea id=\"todoDescription\" rows=\"3\" placeholder=\"If no input data is provided below, the AI will execute based on this description." style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px; resize:vertical;\"></textarea>' +
			'</div>' +
			
			// 优先级
			'<div style=\"margin-bottom:15px;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">⚡ priority</label>' +
			'<select id=\"todoPriority\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'<option value=\"low\">🟢 low 低优先级 - 不紧急的任务</option>' +
			'<option value=\"normal\" selected>🟡 normal 普通优先级 - 正常任务</option>' +
			'<option value=\"high\">🟠 high  高优先级 - 重要任务</option>' +
			'<option value=\"urgent\">🔴 urgent 紧急 - 最高优先级</option>' +
			'</select>' +
			'</div>' +
			
			// 指定Agent
			'<div style=\"margin-bottom:15px;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">🤖 指定Agent（可选）</label>' +
			'<select id=\"todoAgent\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' + agentOptions + '</select>' +
			'<div style=\"font-size:12px; color:#6c757d; margin-top:5px;\">如果不指定，系统会自动选择最合适的Agent</div>' +
			'</div>' +
			
			// 任务类型选择
			'<div style=\"margin-bottom:15px;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">⏰ 任务类型</label>' +
			'<div style=\"display:flex; gap:10px; flex-wrap:wrap;\">' +
			'<label style=\"display:flex; align-items:center; gap:5px; cursor:pointer;\">' +
			'<input type=\"radio\" name=\"scheduleTypeRadio\" value=\"once\" checked onchange=\"updateScheduleUI()\"> 一次性任务' +
			'</label>' +
			'<label style=\"display:flex; align-items:center; gap:5px; cursor:pointer;\">' +
			'<input type=\"radio\" name=\"scheduleTypeRadio\" value=\"daily\" onchange=\"updateScheduleUI()\"> daily执行' +
			'</label>' +
			'<label style=\"display:flex; align-items:center; gap:5px; cursor:pointer;\">' +
			'<input type=\"radio\" name=\"scheduleTypeRadio\" value=\"weekly\" onchange=\"updateScheduleUI()\"> weekly执行' +
			'</label>' +
			'<label style=\"display:flex; align-items:center; gap:5px; cursor:pointer;\">' +
			'<input type=\"radio\" name=\"scheduleTypeRadio\" value=\"monthly\" onchange=\"updateScheduleUI()\"> monthly执行' +
			'</label>' +
			'<label style=\"display:flex; align-items:center; gap:5px; cursor:pointer;\">' +
			'<input type=\"radio\" name=\"scheduleTypeRadio\" value=\"interval\" onchange=\"updateScheduleUI()\"> interval执行' +
			'</label>' +
			'</div>' +
			'</div>' +
			
			// 动态调度配置区域
			'<div id=\"scheduleConfigArea\" style=\"margin-bottom:15px; padding:15px; background:#f8f9fa; border-radius:8px;\">' +
			// 一次性任务配置（默认显示）
			'<div id=\"onceConfig\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📅 执行时间</label>' +
			'<input type=\"datetime-local\" id=\"runAt\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'<div style=\"font-size:12px; color:#6c757d; margin-top:5px;\">设置任务执行的具体时间，不设置则立即执行</div>' +
			'</div>' +
			
			// 每天执行配置
			'<div id=\"dailyConfig\" style=\"display:none;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">🕐 每天执行时间</label>' +
			'<input type=\"time\" id=\"dailyTime\" value=\"09:00\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'<div style=\"font-size:12px; color:#6c757d; margin-top:5px;\">例如: 09:00 表示每天早上9点执行</div>' +
			'</div>' +
			
			// 每周执行配置
			'<div id=\"weeklyConfig\" style=\"display:none;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📅 选择星期几</label>' +
			'<select id=\"weeklyDay\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'<option value=\"0\">Sunday</option>' +
			'<option value=\"1\">Monday</option>' +
			'<option value=\"2\">Tuesday</option>' +
			'<option value=\"3\">Wednesday</option>' +
			'<option value=\"4\">Thursday</option>' +
			'<option value=\"5\">Friday</option>' +
			'<option value=\"6\">Saturday</option>' +
			'</select>' +
			'<label style=\"display:block; margin-top:10px; margin-bottom:5px; font-weight:500;\">🕐 执行时间</label>' +
			'<input type=\"time\" id=\"weeklyTime\" value=\"09:00\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'</div>' +
			
			// 每月执行配置
			'<div id=\"monthlyConfig\" style=\"display:none;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📅 选择日期（1-31）</label>' +
			'<input type=\"number\" id=\"monthlyDay\" min=\"1\" max=\"31\" value=\"1\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'<label style=\"display:block; margin-top:10px; margin-bottom:5px; font-weight:500;\">🕐 执行时间</label>' +
			'<input type=\"time\" id=\"monthlyTime\" value=\"09:00\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'<div style=\"font-size:12px; color:#6c757d; margin-top:5px;\">注意：如果选择31号，但当月没有31号，将在当月最后一天执行</div>' +
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
			'<div style=\"font-size:12px; color:#6c757d; margin-top:5px;\">⚠️ 建议间隔不要小于10分钟，避免任务执行过于频繁</div>' +
			'</div>' +
			'</div>' +
			
			// 输入数据说明
			'<div style=\"margin-bottom:15px;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">📤 传递给AI的输入数据</label>' +
			'<textarea id=\"todoInputData\" rows=\"4\" placeholder=\"例如:&#10;请帮我总结今天的财经新闻，包括：&#10;1. A股市场表现&#10;2. 重要政策动态&#10;3. 热点板块分析\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px; resize:vertical;\"></textarea>' +
			'<div style=\"font-size:12px; color:#6c757d; margin-top:5px;\">💡 这是你希望AI执行的具体任务描述，描述越清晰，AI执行效果越好</div>' +
			'</div>' +
			
			// 重试次数
			'<div style=\"margin-bottom:15px;\">' +
			'<label style=\"display:block; margin-bottom:5px; font-weight:500;\">🔄 失败重试次数</label>' +
			'<input type=\"number\" id=\"todoMaxRetries\" min=\"0\" max=\"10\" value=\"3\" style=\"width:100%; padding:10px; border:1px solid #dee2e6; border-radius:6px;\">' +
			'<div style=\"font-size:12px; color:#6c757d; margin-top:5px;\">任务执行失败时的重试次数，建议3次</div>' +
			'</div>' +
			
			// 创建按钮
			'<button type=\"button\" onclick=\"createTodo()\" style=\"width:100%; background: linear-gradient(135deg, #28a745 0%, #20c997 100%); color:white; padding:12px; border:none; border-radius:6px; cursor:pointer; font-size:16px; font-weight:500;\">✅ 创建定时任务</button>' +
			'</div>';
		
		document.getElementById('todoContent').innerHTML = formHtml;
		
		// 初始化调度UI
		updateScheduleUI();
	}

	function updateScheduleUI() {
		var radios = document.getElementsByName('scheduleTypeRadio');
		var selectedValue = '';
		for (var i = 0; i < radios.length; i++) {
			if (radios[i].checked) {
				selectedValue = radios[i].value;
				break;
			}
		}
		
		// 隐藏所有配置面板
		var onceConfig = document.getElementById('onceConfig');
		var dailyConfig = document.getElementById('dailyConfig');
		var weeklyConfig = document.getElementById('weeklyConfig');
		var monthlyConfig = document.getElementById('monthlyConfig');
		var intervalConfig = document.getElementById('intervalConfig');
		
		if (onceConfig) onceConfig.style.display = 'none';
		if (dailyConfig) dailyConfig.style.display = 'none';
		if (weeklyConfig) weeklyConfig.style.display = 'none';
		if (monthlyConfig) monthlyConfig.style.display = 'none';
		if (intervalConfig) intervalConfig.style.display = 'none';
		
		// 显示选中的配置面板
		if (selectedValue === 'once') {
			if (onceConfig) onceConfig.style.display = 'block';
		} else if (selectedValue === 'daily') {
			if (dailyConfig) dailyConfig.style.display = 'block';
		} else if (selectedValue === 'weekly') {
			if (weeklyConfig) weeklyConfig.style.display = 'block';
		} else if (selectedValue === 'monthly') {
			if (monthlyConfig) monthlyConfig.style.display = 'block';
		} else if (selectedValue === 'interval') {
			if (intervalConfig) intervalConfig.style.display = 'block';
		}
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
			const response = await fetch('/api/todo/list?limit=50');
			const data = await response.json();
			const todos = data.todos || [];
			const statusText = { pending: '⏳待执行', running: '🔄执行中', completed: '✅已完成', failed: '❌失败', cancelled: '⏹️已取消' };
			
			var html = '';
			for (var i = 0; i < todos.length; i++) {
				var t = todos[i];
				html += '<div class="todo-item ' + t.status + '">' +
					'<div class="todo-title">' + escapeHtml(t.title) + '</div>' +
					'<div class="todo-meta">状态: ' + (statusText[t.status] || t.status) + ' | 执行次数: ' + (t.run_count || 0) + '</div>' +
					'<button onclick="cancelTodo(\'' + t.todo_id + '\')" style="background:#dc3545; color:white; border:none; border-radius:4px; padding:4px 8px;">取消</button>' +
					'</div>';
			}
			
			document.getElementById('todoContent').innerHTML = html;
			if (todos.length === 0) {
				document.getElementById('todoContent').innerHTML = '<div style="text-align:center; padding:40px;">暂无TODO任务</div>';
			}
		} catch(err) { 
			document.getElementById('todoContent').innerHTML = '<div style="color:#dc3545;">加载失败</div>'; 
		}
	}
    
    async function cancelTodo(todoId) {
		if (!confirm('确定要取消这个任务吗？')) return;
		try {
			// 使用字符串拼接，不要用反引号模板字符串
			const response = await fetch('/api/todo/' + todoId, { method: 'DELETE' });
			if (response.ok) {
				alert('任务已取消');
				showTodoList();
			} else {
				alert('取消失败');
			}
		} catch (err) {
			alert('取消失败: ' + err.message);
		}
	}

	async function saveConfig() {
		const form = document.getElementById('configForm');
		// 触发表单提交事件
		const event = new Event('submit', { bubbles: true, cancelable: true });
		form.dispatchEvent(event);
		setTimeout(() => {
			closeConfigModal();
			addLog('info', '配置已保存，模态框已关闭');
		}, 500);
	}
    
    // 文件预览功能
   async function previewFile(fileName) {
		var filePath = currentFilePath === '/' ? fileName : currentFilePath + '/' + fileName;
		var ext = fileName.split('.').pop().toLowerCase();
		
		var modal = document.getElementById('previewModal');
		var previewBody = document.getElementById('previewBody');
		var previewTitle = document.getElementById('previewTitle');
		
		previewTitle.textContent = '预览: ' + fileName;
		previewBody.innerHTML = '<div style="text-align:center; padding:40px;">📄 加载中...</div>';
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
						'<div class="line-numbers" style="background:#f6f8fa; padding:12px 8px; text-align:right; color:#6a737d; font-family:Monaco,Menlo,monospace; font-size:13px; line-height:1.5; user-select:none; border-right:1px solid #e1e4e8;">' +
							'<pre style="margin:0; padding:0;">' + lineNumbers + '</pre>' +
						'</div>' +
						'<div style="flex:1; overflow-x:auto;">' +
							'<pre style="margin:0; padding:12px; background:#f6f8fa; font-family:Monaco,Menlo,monospace; font-size:13px; line-height:1.5;"><code class="language-' + language + '" style="background:transparent; padding:0;">' + escapeHtml(text) + '</code></pre>' +
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
				infoBar.style.cssText = 'margin-bottom:12px; padding:8px 12px; background:#eef2f5; border-radius:8px; font-size:12px; color:#586069; display:flex; justify-content:space-between;';
				infoBar.innerHTML = 
					'<span>📄 文件类型: ' + ext.toUpperCase() + '</span>' +
					'<span>📏 行数: ' + lines.length + '</span>' +
					'<span>📊 大小: ' + formatFileSize(text.length) + '</span>' +
					'<span>🔤 编码: UTF-8</span>';
				previewBody.insertBefore(infoBar, previewBody.firstChild);
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
						'<pre style="margin:0; white-space:pre-wrap; font-family:Monaco,Menlo,monospace; font-size:13px; line-height:1.5;">' + escapeHtml(text) + '</pre>' +
					'</div>';
			}
			// 不支持预览的文件类型
			else {
				previewBody.innerHTML = 
					'<div style="text-align:center; padding:60px 20px;">' +
						'<div style="font-size:48px; margin-bottom:16px;">📄</div>' +
						'<div style="font-size:16px; color:#586069; margin-bottom:12px;">不支持预览 .' + ext + ' 文件</div>' +
						'<div style="font-size:14px; color:#6a737d;">支持预览的类型: 代码文件(py/go/c/java/js等)、Markdown、PDF、图片、文本文件</div>' +
						'<a href="' + downloadUrl + '" download="' + fileName + '" style="display:inline-block; margin-top:20px; padding:8px 20px; background:#28a745; color:white; text-decoration:none; border-radius:6px;">📥 下载文件</a>' +
					'</div>';
			}
		} catch (err) {
			console.error('预览失败:', err);
			previewBody.innerHTML = 
				'<div style="text-align:center; padding:40px; color:#dc3545;">' +
					'<div style="font-size:48px; margin-bottom:16px;">❌</div>' +
					'<div>预览失败: ' + escapeHtml(err.message) + '</div>' +
					'<div style="margin-top:16px; font-size:13px; color:#6c757d;">可能是文件过大或格式不支持</div>' +
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
			'    font-size: 13px;' +
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
        document.getElementById('previewModal').classList.remove('active');
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
        // session_ 开头的是 web 会话，可编辑
        if (currentSessionId.startsWith('session_')) return true;
        // 其他渠道的会话（如 qq_xxx, feishu_xxx）只读
        return false;
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
		currentFilePath = '/';
		await loadFileList('/');
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
            container.innerHTML = '<div style="text-align: center; padding: 40px; color: #adb5bd;">📂 没有文件</div>';
            return;
        }
        
        var html = '';
        for (var i = 0; i < filteredFileList.length; i++) {
            var file = filteredFileList[i];
            var isDir = file.is_dir;
            var icon = isDir ? '📁' : getFileIcon(file.name);
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
                (!isDir ? '<button class="download-btn" onclick="event.stopPropagation(); downloadFile(\'' + escapeHtml(file.name) + '\')">📥 下载</button>' : '') +
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
			statusDiv.innerHTML = '📥 正在下载: ' + escapeHtml(fileName) + '...';
			
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
					this.style.transform = 'scale(0.95)';
					setTimeout(() => {
						this.style.transform = '';
					}, 150);
				}
			});
		});
	}

    
	

	// 过滤文件列表
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
		var iconMap = {
			'txt': '📄',
			'md': '📝',
			'json': '🔧',
			'js': '💛',
			'py': '🐍',
			'go': '🔵',
			'java': '☕',
			'html': '🌐',
			'css': '🎨',
			'jpg': '🖼️',
			'png': '🖼️',
			'gif': '🎬',
			'pdf': '📕',
			'zip': '📦',
			'tar': '📦',
			'gz': '📦'
		};
		return iconMap[ext] || '📄';
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
    
    function filterLogs() {
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
    
    function openLogModal() {
        renderLogs();
        document.getElementById('logModal').classList.add('active');
    }
    
    function closeLogModal() {
        document.getElementById('logModal').classList.remove('active');
    }
    
    // 侧边栏折叠
    function toggleSidebar() {
        document.getElementById('sidebar').classList.toggle('collapsed');
    }
    
    // 模式切换
    function switchMode(mode) {
        currentMode = mode;
        var btns = document.querySelectorAll('.mode-btn');
        for (var i = 0; i < btns.length; i++) {
            btns[i].classList.remove('active');
        }
        if (mode === 'single') {
            document.querySelector('.mode-btn[onclick="switchMode(\'single\')"]').classList.add('active');
            document.getElementById('agentSelector').style.display = 'flex';
            document.getElementById('messageInput').placeholder = '输入消息... (Shift+Enter 换行，Enter 发送)';
        } else {
            document.querySelector('.mode-btn[onclick="switchMode(\'collab\')"]').classList.add('active');
            document.getElementById('agentSelector').style.display = 'none';
            document.getElementById('messageInput').placeholder = '输入讨论主题，点击发送后将打开协同配置...';
        }
    }
    
    document.addEventListener('DOMContentLoaded', function() {
		console.log('DOM 加载完成，开始初始化...');
		
		// 恢复之前的 session_id
		var savedSessionId = localStorage.getItem('currentSessionId');
		if (savedSessionId) {
			currentSessionId = savedSessionId;
			loadSession(savedSessionId);
		}
		
		// 初始化用户ID
		currentUserId = getUserId();
		addLog('info', '用户ID: ' + currentUserId);
		
		// 加载 Agents 和 Sessions
		loadAgents();
		loadSessions();
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
	});

	window.addEventListener('beforeunload', function() {
		if (eventSource) {
			eventSource.close();
		}
	});
    
	function handleEnterKey(e) {
		// 检查是否按下了 Enter 键
		if (e.key === 'Enter') {
			// 检查是否同时按下了 Shift 键
			if (!e.shiftKey) {
				// 阻止默认行为（换行）
				e.preventDefault();
				
				// 获取输入框内容
				var input = document.getElementById('messageInput');
				if (!input) return;
				
				var message = input.value.trim();
				
				// 如果有内容且不在加载中，则发送消息
				if (message && !isLoading) {
					console.log('Enter 键触发发送消息');
					addLog('info', '按 Enter 键发送消息');
					sendMessage();
				} else if (isLoading) {
					addLog('info', '正在处理中，请稍后再试');
					// 可选：添加视觉反馈
					var sendBtn = document.getElementById('sendBtn');
					if (sendBtn) {
						sendBtn.style.transform = 'scale(0.95)';
						setTimeout(() => {
							sendBtn.style.transform = '';
						}, 200);
					}
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
    
    async function loadAgents() {
        try {
            var response = await fetch('/api/agents');
            var data = await response.json();
            agents = data.agents || [];
            
            var select = document.getElementById('agentSelect');
            var options = '<option value="">默认 Agent</option>';
            for (var i = 0; i < agents.length; i++) {
                options += '<option value="' + agents[i].name + '">' + agents[i].name + ' (' + agents[i].type + ')</option>';
            }
            select.innerHTML = options;
            
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
    
    async function loadSession(sessionId) {
		currentSessionId = sessionId;
		localStorage.setItem('currentSessionId', sessionId);
		
		// 判断会话来源
		if (sessionId.startsWith('session_')) {
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
			var response = await fetch('/api/session/' + sessionId);
			var data = await response.json();
			var messages = data.messages || [];
			
			var container = document.getElementById('messagesArea');
			if (messages.length === 0) {
				container.innerHTML = '<div style="text-align: center; color: #adb5bd; padding: 40px;">开始对话吧！</div>';
				return;
			}
			
			var html = '';
			for (var i = 0; i < messages.length; i++) {
				html += renderMessage(messages[i]);
			}
			container.innerHTML = html;
			// 追加任务耗时到最后一个助手气泡（仅当前活跃会话）
			if (window.lastTaskStartTime && currentSessionId && sessionId === currentSessionId) {
				var elapsed = ((Date.now() - window.lastTaskStartTime) / 1000).toFixed(1);
				var infos = container.querySelectorAll('.message.assistant .message-info');
				if (infos.length > 0) {
					infos[infos.length - 1].innerHTML += ' <span style="color:#667eea;font-weight:600;font-size:11px;">⏱ ' + elapsed + 's</span>';
				}
				delete window.lastTaskStartTime;
			}
			scrollToBottom();
			
			// 更新会话标题（可选）
			var sessionTitle = document.getElementById('sessionTitle');
			if (sessionTitle && messages.length > 0) {
				// 使用第一条用户消息作为标题预览
				var firstUserMsg = messages.find(function(m) { return m.role === 'user'; });
				if (firstUserMsg && firstUserMsg.content) {
					var shortTitle = firstUserMsg.content.substring(0, 15);
					if (firstUserMsg.content.length > 15) shortTitle += '...';
					sessionTitle.textContent = shortTitle;
				} else {
					sessionTitle.textContent = 'AI Agent 对话';
				}
			}
			
			loadSessions();
			addLog('info', '加载会话: ' + sessionId + ' (来源: ' + currentSessionSource + ', 消息数: ' + messages.length + ')');
		} catch (err) {
			console.error('加载消息失败:', err);
			addLog('error', '加载消息失败: ' + err.message);
		}
	}

	function resetSendingState() {
		isLoading = false;
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
		
	async function loadSessions() {
        try {
            var response = await fetch('/api/sessions');
            var data = await response.json();
            var sessions = data.sessions || [];
            
            var container = document.getElementById('sessionsList');
            if (sessions.length === 0) {
                container.innerHTML = '<div style="text-align: center; color: #adb5bd; padding: 20px;">暂无对话</div>';
                return;
            }
            
            var html = '';
            for (var i = 0; i < sessions.length; i++) {
                var s = sessions[i];
                var activeClass = (currentSessionId === s.id) ? 'active' : '';
                var sessionName = s.name || s.id.substring(0, 12);
                var messageCount = s.message_count || 0;
                var updateTime = formatDate(s.updated_at);
                
                // 判断会话来源
                var sourceIcon = '🌐';
                if (s.id.startsWith('qq_')) sourceIcon = '🐧';
                else if (s.id.startsWith('feishu_')) sourceIcon = '🐦';
                else if (s.id.startsWith('dingtalk_')) sourceIcon = '📱';
                else if (s.id.startsWith('wework_')) sourceIcon = '💼';
                
                html += '<div class="session-item ' + activeClass + '" onclick="loadSession(\'' + s.id + '\')">' +
                    '<div class="session-name">' + sourceIcon + ' ' + escapeHtml(sessionName) + '</div>' +
                    '<div class="session-time">' + messageCount + ' 条消息  ' + updateTime + '</div>' +
                    '</div>';
            }
            container.innerHTML = html;
        } catch (err) {
            console.error('加载会话失败:', err);
            addLog('error', '加载会话失败: ' + err.message);
        }
    }
    
    function renderMessage(msg) {
		var isUser = msg.role === 'user';
		// 先解码 HTML 实体，再转义防止 XSS
		var rawContent = msg.content || '';
		var decodedContent = rawContent
			.replace(/&#39;/g, "'")
			.replace(/&quot;/g, '"')
			.replace(/&lt;/g, '<')
			.replace(/&gt;/g, '>')
			.replace(/&amp;/g, '&');
		
		var time = formatDate(msg.created_at);
		var agentName = msg.agent_name || (isUser ? '我' : 'AI助手');
		
		// 渠道标识
		var channelBadge = '';
		if (msg.channel && msg.channel !== 'web') {
			var channelIcons = {
				'qq': '🐧',
				'feishu': '🐦',
				'dingtalk': '📱',
				'wework': '💼'
			};
			var icon = channelIcons[msg.channel] || '📡';
			channelBadge = '<span style="font-size:10px; background:#e9ecef; padding:2px 6px; border-radius:10px; margin-left:8px;">' + icon + ' ' + msg.channel + '</span>';
		}
		
		// 判断是否是 Markdown
		var isMarkdown = !isUser && (decodedContent.includes('##') || decodedContent.includes('**'));
		
		var renderedContent;
		if (isMarkdown) {
			try {
				renderedContent = marked.parse(decodedContent);
			} catch (e) {
				renderedContent = escapeHtml(decodedContent).replace(/\n/g, '<br>');
			}
		} else {
			renderedContent = escapeHtml(decodedContent).replace(/\n/g, '<br>');
		}
		
		return '<div class="message ' + msg.role + '">' +
			'<div class="message-bubble">' +
			(!isUser ? '<div class="agent-name">' + escapeHtml(agentName) + channelBadge + '</div>' : '') +
			'<div class="message-content">' + renderedContent + '</div>' +
			'<div class="message-info">' + time + '</div>' +
			'</div>' +
			'</div>';
	}
    
    async function createNewSession() {
		// 生成格式化时间：年后两位+月+日+时+分+秒
		const now = new Date();
		const year = now.getFullYear().toString().slice(2);  // 26
		const month = (now.getMonth() + 1).toString().padStart(2, '0');  // 03
		const day = now.getDate().toString().padStart(2, '0');  // 27
		const hour = now.getHours().toString().padStart(2, '0');
		const minute = now.getMinutes().toString().padStart(2, '0');
		const second = now.getSeconds().toString().padStart(2, '0');
		
		// 最终格式：session_260327153025_xxxxxxx
		const timeStr = year + month + day + hour + minute + second;
		currentSessionId = 'session_' + timeStr + '_' + Math.random().toString(36).substr(2, 4);
		currentSessionSource = 'web';
		
		// 保存到 localStorage
		localStorage.setItem('currentSessionId', currentSessionId);
		
		document.getElementById('sessionTitle').textContent = '新对话';
		document.getElementById('messagesArea').innerHTML = '<div style="text-align: center; color: #adb5bd; padding: 40px;">开始对话吧！</div>';
		loadSessions();
		addLog('info', '创建新会话: ' + currentSessionId);
	}
    
	// File upload state
	let uploadedFilePath = null;
	let uploadedFileName = null;

	async function handleFileUpload(input) {
		const file = input.files[0];
		if (!file) return;

		const formData = new FormData();
		formData.append('file', file);

		try {
			const resp = await fetch('/api/upload', { method: 'POST', body: formData });
			const data = await resp.json();
			if (data.success) {
				uploadedFilePath = data.path;
				uploadedFileName = data.filename;
				document.getElementById('uploadFileName').textContent = '📎 ' + data.filename + ' (' + formatFileSize(data.size) + ')';
				document.getElementById('uploadArea').style.display = 'block';
				addLog('info', '文件已上传: ' + data.filename);
			} else {
				addLog('error', '上传失败: ' + (data.error || '未知错误'));
			}
		} catch (e) {
			addLog('error', '上传失败: ' + e.message);
		}
		input.value = '';
	}

	function clearUpload() {
		uploadedFilePath = null;
		uploadedFileName = null;
		document.getElementById('uploadArea').style.display = 'none';
		document.getElementById('uploadFileName').textContent = '';
	}

	function formatFileSize(bytes) {
		if (bytes < 1024) return bytes + 'B';
		if (bytes < 1024*1024) return (bytes/1024).toFixed(1) + 'KB';
		return (bytes/(1024*1024)).toFixed(1) + 'MB';
	}

	// Click the hidden file input when upload button is clicked
	document.addEventListener('DOMContentLoaded', function() {
		var uploadBtn = document.getElementById('uploadBtn');
		var fileInput = document.getElementById('fileInput');
		if (uploadBtn && fileInput) {
			uploadBtn.addEventListener('click', function() { fileInput.click(); });
		}
	});

    async function sendMessage() {
		if (isLoading) return;
		if (!isSessionEditable()) {
			alert('此会话来自其他渠道，仅可查看历史消息，不能发送新消息');
			return;
		}
		
		var input = document.getElementById('messageInput');
		var message = input.value.trim();
		if (!message) return;

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
		
		if (currentMode === 'collab') {
			openCollabModal(message);
			input.value = '';
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
			created_at: new Date().toISOString(),
			agent_name: ''
		};
		
		var container = document.getElementById('messagesArea');
		if (container.innerHTML.indexOf('开始对话吧') !== -1) {
			container.innerHTML = '';
		}
		container.innerHTML += renderMessage(userMsg);
		scrollToBottom();
		
		input.value = '';
		isLoading = true;
		document.getElementById('sendBtn').disabled = true;
		window.lastTaskStartTime = Date.now();  // 记录任务开始时间，用于完成后显示耗时
		
		// 创建 loading 消息，使用唯一 ID
		var loadingId = 'loading_' + Date.now() + '_' + Math.random().toString(36).substr(2, 8);
		var agentName = document.getElementById('agentSelect').value || 'AI助手';
		var loadingHtml = '<div class="message assistant" id="' + loadingId + '">' +
			'<div class="message-bubble">' +
			'<div class="agent-name">' + escapeHtml(agentName) + '</div>' +
			'<div class="loading-top" style="display:flex;align-items:center;gap:8px;margin-bottom:6px;">' +
			'<div class="typing-indicator"><span></span><span></span><span></span></div>' +
			'<span class="loading-text" style="font-size:12px;color:#6c757d;">正在思考中...</span>' +
			'</div>' +
			'<div class="progress-timeline" style="text-align:left;max-height:180px;overflow-y:auto;font-size:11px;max-width:100%;word-break:break-all;display:none;"></div>' +
			'</div>' +
			'</div>';
		
		container.innerHTML += loadingHtml;
		scrollToBottom();
		
		addLog('info', '发送消息 (SessionId: ' + currentSessionId + '): ' + message.substring(0, 50));
		
		try {
			var agentName = document.getElementById('agentSelect').value;
			// Include uploaded file reference in the message
			var finalMessage = message;
			if (uploadedFilePath) {
				finalMessage = '📎 [用户上传了文件: ' + uploadedFileName + ', 路径: ' + uploadedFilePath + ']\n请先读取并分析这个文件的内容，然后处理用户的请求。\n\n用户消息:\n' + message;
				clearUpload();
			}

			var requestData = {
				task_type: 'chat',
				agent_name: agentName,
				message: finalMessage,
				session_id: currentSessionId,
				channel: 'web',
				user_id: getUserId()
			};
			
			var response = await fetch('/api/chat', {
				method: 'POST',
				headers: {
					'Content-Type': 'application/json; charset=utf-8'
				},
				body: JSON.stringify(requestData)
			});
			
			if (!response.ok) {
				throw new Error('HTTP ' + response.status);
			}

			var result = await response.json();
			
			if (result.task_id) {
				// 保存任务 ID 和 loading ID 的映射
				window.pendingTasks = window.pendingTasks || {};
				window.pendingTasks[result.task_id] = loadingId;
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
				}, 30000);
				
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
				isLoading = false;
				document.getElementById('sendBtn').disabled = false;
			}
			
			loadSessions();
		} catch (err) {
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
			isLoading = false;
			document.getElementById('sendBtn').disabled = false;
		}
	}

    
    async function pollTaskResult(taskId, agentName) {
        for (var i = 0; i < 60; i++) {
            await new Promise(function(resolve) { setTimeout(resolve, 1000); });
            
            try {
                var response = await fetch('/api/task/' + taskId);
                var data = await response.json();
                
                if (data.status === 'completed') {
                    var assistantMsg = {
                        role: 'assistant',
                        content: typeof data.result === 'string' ? data.result : JSON.stringify(data.result, null, 2),
                        created_at: new Date().toISOString(),
                        agent_name: agentName
                    };
                    var container = document.getElementById('messagesArea');
                    container.innerHTML += renderMessage(assistantMsg);
                    scrollToBottom();
                    break;
                } else if (data.status === 'failed') {
                    var errorMsg = {
                        role: 'assistant',
                        content: '任务失败: ' + (data.error || '未知错误'),
                        created_at: new Date().toISOString(),
                        agent_name: '系统'
                    };
                    var container = document.getElementById('messagesArea');
                    container.innerHTML += renderMessage(errorMsg);
                    scrollToBottom();
                    addLog('error', '任务失败: ' + (data.error || '未知错误'));
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
		
		// 填充主持人选择下拉框
		var hostSelect = document.getElementById('hostAgent');
		hostSelect.innerHTML = '<option value="">请选择主持人</option>';
		for (var i = 0; i < agents.length; i++) {
			hostSelect.innerHTML += '<option value="' + agents[i].name + '">' + agents[i].name + ' (' + agents[i].type + ')</option>';
		}
		
		document.getElementById('collabModal').classList.add('active');
	}

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
    
    function closeCollabModal() {
        document.getElementById('collabModal').classList.remove('active');
    }
    
    async function startCollaboration() {
		var mode = document.querySelector('input[name="collabMode"]:checked').value;
		var selectedAgents = [];
		var checkboxes = document.querySelectorAll('#collabAgentsList input:checked');
		for (var i = 0; i < checkboxes.length; i++) {
			selectedAgents.push(checkboxes[i].value);
		}
		
		if (selectedAgents.length === 0) {
			alert('请至少选择一个Agent');
			return;
		}
		
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
		
		var requestData = {
			task_type: 'collaboration',
			message: message,
			collaboration: collabConfig,
			parameters: {
				team_name: '协同团队_' + Date.now()
			}
		};
		
		document.getElementById('collabProgress').style.display = 'block';
		document.getElementById('progressFill').style.width = '0%';
		document.getElementById('collabStatus').innerHTML = '🚀 准备开始...';
		closeCollabModal();
		
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
				pollCollaborationProgress(result.task_id);
			}
		} catch (err) {
			console.error('启动协同失败:', err);
			addLog('error', '启动协同失败: ' + err.message);
			document.getElementById('collabStatus').innerHTML = '❌ 启动失败: ' + err.message;
			alert('启动协同失败: ' + err.message);
		}
	}

    
    async function pollCollaborationProgress(taskId) {
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
					for (var i = 0; i < data.result.responses.length; i++) {
						var resp = data.result.responses[i];
						statusHtml += '  • ' + resp.agent + ': ' + resp.response.substring(0, 100) + '...<br>';
					}
				}
				
				document.getElementById('collabStatus').innerHTML = statusHtml;
				
				if (data.status === 'completed') {
					completed = true;
					document.getElementById('progressFill').style.width = '100%';
					document.getElementById('collabStatus').innerHTML += '<br>✅ 协同完成！';
					addLog('info', '协同任务完成: ' + taskId);
					
					// 显示结果
					var resultMsg = {
						role: 'assistant',
						content: '🎉 协同任务完成！\n\n📋 结果摘要:\n' + formatCollaborationResult(data.result),
						created_at: new Date().toISOString(),
						agent_name: '协同系统'
					};
					var container = document.getElementById('messagesArea');
					if (container.innerHTML.indexOf('开始对话吧') !== -1) {
						container.innerHTML = '';
					}
					container.innerHTML += renderMessage(resultMsg);
					scrollToBottom();
					
					setTimeout(function() {
						document.getElementById('collabProgress').style.display = 'none';
					}, 5000);
					break;
				} else if (data.status === 'failed') {
					completed = true;
					document.getElementById('collabStatus').innerHTML += '<br>❌ 协同失败: ' + (data.error || '未知错误');
					addLog('error', '协同失败: ' + (data.error || '未知错误'));
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
			switchConfigTab(defaultTab);
		} catch (err) {
			console.error('打开配置失败:', err);
			addLog('error', '打开配置失败: ' + err.message);
		}
	}
    
    function closeConfigModal() {
        document.getElementById('configModal').classList.remove('active');
    }
    
    
    
    async function reloadConfig() {
        try {
            var response = await fetch('/api/config/reload', { method: 'POST' });
            if (response.ok) {
                alert('配置已重新加载');
                addLog('info', '配置已重新加载');
            } else {
                alert('重新加载失败');
            }
        } catch (err) {
            alert('重新加载失败: ' + err.message);
        }
    }
    
    function escapeHtml(str) {
		if (!str) return '';
		// 使用更安全的转义方式，只转义 HTML 特殊字符
		var div = document.createElement('div');
		div.textContent = str;
		return div.innerHTML;
	}
    
    function formatDate(dateStr) {
        if (!dateStr) return '';
        var date = new Date(dateStr);
        var now = new Date();
        var diff = now - date;
        
        if (diff < 60000) return '刚刚';
        if (diff < 3600000) return Math.floor(diff / 60000) + '分钟前';
        if (diff < 86400000) return Math.floor(diff / 3600000) + '小时前';
        return date.toLocaleDateString();
    }
    
    function scrollToBottom() {
        var container = document.getElementById('messagesArea');
        container.scrollTop = container.scrollHeight;
    }

    let eventSource = null;
	let reconnectAttempts = 0;
	const maxReconnectAttempts = 5;


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
		});
		
		eventSource.addEventListener('task_update', function(e) {
			const data = JSON.parse(e.data);
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
						textSpan.textContent = 'AI 正在处理中...';
					}
					
					// 可选：添加进度提示
					var wrapper = loadingEl.querySelector('.typing-wrapper');
					if (wrapper && !wrapper.querySelector('.progress-hint')) {
						var hint = document.createElement('div');
						hint.className = 'progress-hint';
						hint.style.cssText = 'font-size: 10px; color: #999; margin-top: 4px;';
						hint.textContent = '⚙️ 任务执行中';
						wrapper.appendChild(hint);
					}
				}
				
			} else if (data.status === 'completed') {
				addLog('info', '任务 ' + data.task_id + ' 已完成');

					// Save elapsed time to show on final message after reload
					if (window.pendingTaskTimes && window.pendingTaskTimes[data.task_id]) {
					loadingEl.remove();
				}
				
				// 清理映射
				if (window.pendingTasks) {
					delete window.pendingTasks[data.task_id];
				}
				
				// 重要：刷新当前会话的消息
				if (currentSessionId) {
					console.log('任务完成，刷新会话消息:', currentSessionId);
					// 使用 await 方式，确保消息加载完成后再重置状态
					(async function() {
						try {
							await loadSession(currentSessionId);
							console.log('会话消息刷新完成');
						} catch (err) {
							console.error('刷新会话失败:', err);
						} finally {
							// 确保在任何情况下都重置状态
							isLoading = false;
							var sendBtn = document.getElementById('sendBtn');
							if (sendBtn) {
								sendBtn.disabled = false;
							}
							console.log('状态已重置: isLoading = false, sendBtn 已启用');
						}
					})();
				} else {
					// 没有会话时也重置状态
					isLoading = false;
					var sendBtn = document.getElementById('sendBtn');
					if (sendBtn) {
						sendBtn.disabled = false;
					}
				}
				
			} else if (data.status === 'failed') {
				addLog('error', '任务 ' + data.task_id + ' 失败: ' + (data.error || '未知错误'));
				
				// 移除 loading 动画并显示错误
				if (loadingEl && loadingEl.parentNode) {
					// 替换为错误消息而不是直接移除
					var errorMsgDiv = document.createElement('div');
					errorMsgDiv.className = 'message assistant';
					errorMsgDiv.innerHTML = '<div class="message-bubble" style="background: #fff3cd; color: #856404;">' +
						'<div class="agent-name">系统</div>' +
						'<div>❌ 处理失败: ' + escapeHtml(data.error || '未知错误') + '</div>' +
						'<div class="message-info">' + formatDate(new Date()) + '</div>' +
						'</div>';
					loadingEl.parentNode.replaceChild(errorMsgDiv, loadingEl);
				}
				
				// 清理映射
				if (window.pendingTasks) {
					delete window.pendingTasks[data.task_id];
				}
				
				// 重置状态
				isLoading = false;
				var sendBtn = document.getElementById('sendBtn');
				if (sendBtn) {
					sendBtn.disabled = false;
				}
			}
			
			// 其他会话的任务更新 - 只有不是当前任务时才刷新
			if (data.status === 'completed' && (!window.pendingTasks || !window.pendingTasks[data.task_id])) {
				if (currentSessionId) {
					console.log('其他任务完成，刷新会话:', currentSessionId);
					loadSession(currentSessionId);
				}
			}
		});

		// Progress event: real-time agent thinking/tool execution updates
		eventSource.addEventListener('progress', function(e) {
			const data = JSON.parse(e.data);
			console.log('Progress:', data.type, data.message);

			// Find the loading element for this task
			const loadingId = window.pendingTasks ? window.pendingTasks[data.task_id] : null;
			const loadingEl = loadingId ? document.getElementById(loadingId) : null;

			if (!loadingEl) return;

			// Show the existing progress-timeline (already in HTML, hidden by default)
			var timeline = loadingEl.querySelector('.progress-timeline');
			if (!timeline) return;
			timeline.style.display = 'block';

			// Create progress bubble
			var bubble = document.createElement('div');
			bubble.className = 'progress-bubble';
			var icon = '🔄';
			var bg = '#e8f4fd';
			switch(data.type) {
				case 'tool_start':
					icon = '🔧';
					bg = '#fff3cd';
					break;
				case 'tool_end':
					icon = data.status === 'success' ? '✅' : '❌';
					bg = data.status === 'success' ? '#d4edda' : '#f8d7da';
					break;
				case 'llm_call':
					icon = '🤖';
					bg = '#e8f4fd';
					break;
				case 'compress':
					icon = '📦';
					bg = '#e2e3e5';
					break;
				case 'error':
					icon = '⚠️';
					bg = '#f8d7da';
					break;
				case 'complete':
					icon = '🎯';
					bg = '#d4edda';
					break;
				default:
					icon = '💭';
			}
			bubble.style.cssText = 'padding:3px 8px; margin:2px 0; border-radius:4px; background:' + bg + '; color:#333; max-width:400px; overflow:hidden; text-overflow:ellipsis; white-space:nowrap;';
			var msg = data.message || '';
			var truncatedMsg = msg.length > 80 ? msg.substring(0, 80) + '...' : msg;
			bubble.innerHTML = icon + ' ' + escapeHtml(truncatedMsg);
			timeline.appendChild(bubble);
			timeline.scrollTop = timeline.scrollHeight;

			// Update the loading text to show current action
			var textSpan = loadingEl.querySelector('.loading-text');
			if (textSpan && data.message) {
				var shortMsg = data.message.length > 50 ? data.message.substring(0, 50) + '...' : data.message;
				textSpan.textContent = icon + ' ' + shortMsg;
			}
		});

		eventSource.onerror = function(e) {
			console.error('SSE 连接错误:', e);
			addLog('error', '实时推送连接断开，尝试重连...');
			
			if (eventSource) {
				eventSource.close();
			}
			
			reconnectAttempts++;
			if (reconnectAttempts <= maxReconnectAttempts) {
				setTimeout(initSSE, 3000 * reconnectAttempts);
			}
		};
	}

	function updateLoadingMessageForTask(taskId, message) {
		const loadingId = window.pendingTasks ? window.pendingTasks[taskId] : null;
		if (loadingId) {
			const loadingEl = document.getElementById(loadingId);
			if (loadingEl) {
				const textSpan = loadingEl.querySelector('.loading-text');
				if (textSpan) {
					textSpan.textContent = message;
				}
			}
		}
	}
	
	function updateLoadingMessage(message) {
		if (currentLoadingId) {
			const loadingEl = document.getElementById(currentLoadingId);
			if (loadingEl) {
				// 更新 loading 动画，添加文字
				loadingEl.innerHTML = '<div class="message-bubble">' +
					'<div style="display: flex; align-items: center; gap: 8px;">' +
					'<div class="typing-indicator" style="display: inline-flex;"><span></span><span></span><span></span></div>' +
					'<span style="font-size: 12px; color: #6c757d;">' + message + '</span>' +
					'</div>' +
					'</div>';
			}
		}
	}	

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
				'<h3 style="margin: 0; color: white; font-size: 18px;">📋 执行日志 - 全屏模式</h3>' +
				'<button onclick="toggleLogFullscreen()" style="padding: 6px 16px; background: #dc3545; color: white; border: none; border-radius: 4px; cursor: pointer; font-size: 14px;">✕ 退出全屏</button>' +
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
				newLogViewer.style.cssText = 'flex: 1; overflow-y: auto; padding: 16px; background: #1e1e1e; font-family: Monaco, Menlo, monospace; font-size: 12px;';
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
						'<span class="log-level ' + levelClass + '" style="display: inline-block; padding: 2px 6px; border-radius: 4px; font-size: 10px; margin-right: 12px;">' + levelText + '</span>' +
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
		// 更新选项卡样式
		var tabs = document.querySelectorAll('.config-tab');
		for (var i = 0; i < tabs.length; i++) {
			tabs[i].classList.remove('active');
			// 重置背景色和内联样式
			tabs[i].style.background = '';
			tabs[i].style.color = '';
		}
		
		// 激活选中的选项卡
		var activeTab = document.querySelector('.config-tab[onclick="switchConfigTab(\'' + tab + '\')"]');
		if (activeTab) {
			activeTab.classList.add('active');
		}
		
		// 显示对应的配置面板
		var sections = document.querySelectorAll('.config-section');
		for (var i = 0; i < sections.length; i++) {
			sections[i].style.display = 'none';
		}
		
		var sectionId = '';
		if (tab === 'basic') sectionId = 'basicConfig';
		else if (tab === 'agents') sectionId = 'agentsConfig';
		else if (tab === 'models') sectionId = 'modelsConfig';
		else if (tab === 'gateway') sectionId = 'gatewayConfig';
		else if (tab === 'email') sectionId = 'emailConfig';
		else if (tab === 'channels') sectionId = 'channelsConfig';
		
		var targetSection = document.getElementById(sectionId);
		if (targetSection) {
			targetSection.style.display = 'block';
		}
		
		// 如果切换到特定面板，加载对应数据
		if (tab === 'agents') {
			renderAgentsList();
		} else if (tab === 'models') {
			renderModelsList();
		} else if (tab === 'channels') {
			if (typeof renderChannelsList === 'function') {
				renderChannelsList();
			} else {
				console.error('renderChannelsList 函数未定义');
			}
		}
	}
    
	async function loadConfigToForm() {
		try {
			const response = await fetch('/api/config/sections');
			if (!response.ok) {
				throw new Error('HTTP ' + response.status);
			}
			currentConfig = await response.json();

			console.log('加载的完整配置:', currentConfig);
            console.log('Channels 配置:', currentConfig.channels);
            				
			// 基础配置
			const teamNameEl = document.getElementById('teamName');
			if (teamNameEl) teamNameEl.value = currentConfig.team_name || '';
			
			const workspaceEl = document.getElementById('workspace');        if (workspaceEl) workspaceEl.value = currentConfig.workspace || '';
			
			const outputDirEl = document.getElementById('outputDir');
			if (outputDirEl) outputDirEl.value = (currentConfig.output && currentConfig.output.dir) || 'storage';
			
			const outputFormatEl = document.getElementById('outputFormat');
			if (outputFormatEl) outputFormatEl.value = (currentConfig.output && currentConfig.output.format) || 'json';
			
			const semaphoreLimitEl = document.getElementById('semaphoreLimit');
			if (semaphoreLimitEl) semaphoreLimitEl.value = (currentConfig.runtime && currentConfig.runtime.semaphore_limit) || 4;
			
			const headlessEl = document.getElementById('headless');
			if (headlessEl) headlessEl.checked = currentConfig.headless || false;
			
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

	function collectConfigData() {
		// 收集模型配置
		const modelConfig = {};
		const modelItems = document.querySelectorAll('.model-config-item');
		
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
		const channels = [];
		const channelItems = document.querySelectorAll('.channel-config-item');
		console.log('找到渠道配置项数量:', channelItems.length);
		for (let i = 0; i < channelItems.length; i++) {
			const item = channelItems[i];
			const nameInput = item.querySelector('.channel-name');
			const typeInput = item.querySelector('.channel-type');
			const enabledCheckbox = item.querySelector('.channel-enabled');
			const configTextarea = item.querySelector('.channel-config');
			
			if (nameInput && nameInput.value) {
				let channelConfig = {};
				try {
					if (configTextarea && configTextarea.value) {
						channelConfig = JSON.parse(configTextarea.value);
					}
				} catch (e) {
					console.error('解析渠道配置失败:', e);
				}
				
				channels.push({
					name: nameInput.value,
					type: typeInput ? typeInput.value : '',
					enabled: enabledCheckbox ? enabledCheckbox.checked : false,
					config: channelConfig
				});
			}
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

	function renderAgentsList() {
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
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">名称</label>' +
				'<input type="text" class="agent-name" value="' + escapeHtml(agent.name || '') + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">类型</label>' +
				'<input type="text" class="agent-type" value="' + escapeHtml(agent.type || 'custom') + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">模型</label>' +
				'<input type="text" class="agent-model" value="' + escapeHtml(agent.model || 'default_model') + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">系统消息</label>' +
				'<textarea class="agent-system-message" data-index="' + i + '" rows="2" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' + escapeHtml(agent.system_message || '') + '</textarea>' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">温度 (0-1)</label>' +
				'<input type="number" step="0.1" class="agent-temperature" value="' + (agent.temperature || 0.7) + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">最大Token</label>' +
				'<input type="number" class="agent-max-tokens" value="' + (agent.max_tokens || 4096) + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">' +
				'<input type="checkbox" class="agent-has-memory" data-index="' + i + '" ' + (agent.has_memory !== false ? 'checked' : '') + '>' +
				' 启用记忆</label>' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">记忆窗口大小</label>' +
				'<input type="number" class="agent-memory-window" value="' + (agent.memory_window || 50) + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'</div>';
		}
		container.innerHTML = html;
	}
	 
	function renderModelsList() {
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
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">模型标识</label>' +
				'<input type="text" class="model-key" value="' + escapeHtml(name) + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">类型 (deepseek/openai/ollama)</label>' +
				'<input type="text" class="model-type" value="' + escapeHtml(model.type || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">API Key</label>' +
				'<input type="text" class="model-api-key" value="' + escapeHtml(model.api_key || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">Base URL</label>' +
				'<input type="text" class="model-base-url" value="' + escapeHtml(model.base_url || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">模型名称</label>' +
				'<input type="text" class="model-model-name" value="' + escapeHtml(model.model_name || '') + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">温度</label>' +
				'<input type="number" step="0.1" class="model-temperature" value="' + (model.temperature || 0.7) + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">最大Token</label>' +
				'<input type="number" class="model-max-tokens" value="' + (model.max_tokens || 4096) + '" data-name="' + escapeHtml(name) + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'</div>';
		}
		container.innerHTML = html;
	}


	// 渲染渠道列表
	function renderChannelsList() {
		var container = document.getElementById('channelsList');
		if (!container) {
			console.error('channelsList 容器不存在');
			return;
		}
		
		// 确保 currentConfig 存在
		if (!currentConfig) {
			console.error('currentConfig 未定义');
			container.innerHTML = '<div style="text-align:center; color:#adb5bd; padding:20px;">配置未加载</div>';
			return;
		}
		
		var channels = currentConfig.channels || [];
		
		console.log('渲染渠道列表，数量:', channels.length);  // 调试输出
		console.log('渠道数据:', channels);  // 调试输出
		
		if (channels.length === 0) {
			container.innerHTML = '<div style="text-align:center; color:#adb5bd; padding:20px;">暂无渠道配置，请添加</div>';
			return;
		}
		
		var html = '';
		for (var i = 0; i < channels.length; i++) {
			var channel = channels[i];
			var configJson = JSON.stringify(channel.config || {}, null, 2);
			
			html += '<div class="channel-config-item" style="border: 1px solid #e9ecef; border-radius: 8px; padding: 12px; margin-bottom: 10px;">' +
				'<div style="display: flex; justify-content: space-between; margin-bottom: 10px;">' +
				'<strong>' + escapeHtml(channel.name || '未命名') + ' (' + escapeHtml(channel.type || 'unknown') + ')</strong>' +
				'<button type="button" onclick="removeChannel(' + i + ')" style="background: #dc3545; color: white; border: none; border-radius: 4px; padding: 2px 8px; cursor: pointer;">删除</button>' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">名称</label>' +
				'<input type="text" class="channel-name" value="' + escapeHtml(channel.name || '') + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">类型 (feishu/qq/dingtalk/wework)</label>' +
				'<input type="text" class="channel-type" value="' + escapeHtml(channel.type || '') + '" data-index="' + i + '" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px;">' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">' +
				'<input type="checkbox" class="channel-enabled" data-index="' + i + '" ' + (channel.enabled ? 'checked' : '') + '>' +
				' 启用</label>' +
				'</div>' +
				'<div class="config-field" style="margin-bottom: 8px;">' +
				'<label style="display:block; font-size:12px; color:#6c757d; margin-bottom:4px;">配置 (JSON)</label>' +
				'<textarea class="channel-config" data-index="' + i + '" rows="6" style="width: 100%; padding: 6px; border:1px solid #dee2e6; border-radius:4px; font-family: monospace; font-size: 11px;">' + escapeHtml(configJson) + '</textarea>' +
				'</div>' +
				'</div>';
		}
		container.innerHTML = html;
	}

	function escapeHtml(str) {
		if (!str) return '';
		return str
			.replace(/&/g, '&amp;')
			.replace(/</g, '&lt;')
			.replace(/>/g, '&gt;')
			.replace(/"/g, '&quot;')
			.replace(/'/g, '&#39;');
	}

	function addAgent() {
		if (!currentConfig.agents) {
			currentConfig.agents = [];
		}
		currentConfig.agents.push({
			name: "new_agent",
			type: "custom",
			model: "default_model",
			system_message: "You are a helpful assistant.",
			temperature: 0.7,
			max_tokens: 4096
		});
		renderAgentsList();
	}

	// 移除Agent
	function removeAgent(index) {
		if (confirm('确定要删除这个Agent吗？')) {
			currentConfig.agents.splice(index, 1);
			renderAgentsList();
		}
	}

	// 添加模型
	function addModel() {
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
	}

	// 移除模型
	function removeModel(name) {
		if (confirm('确定要删除这个模型配置吗？')) {
			delete currentConfig.model_config[name];
			renderModelsList();
		}
	}

	// 添加渠道
	function addChannel() {
		if (!currentConfig.channels) {
			currentConfig.channels = [];
		}
		currentConfig.channels.push({
			name: "new_channel",
			type: "custom",
			enabled: false,
			config: {}
		});
		renderChannelsList();
	}

	// 移除渠道
	function removeChannel(index) {
		if (confirm('确定要删除这个渠道吗？')) {
			currentConfig.channels.splice(index, 1);
			renderChannelsList();
		}
	}
	document.getElementById('configForm').addEventListener('submit', async function(e) {
		e.preventDefault();
		
		const configData = collectConfigData();
		
		try {
			const response = await fetch('/api/config/sections', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(configData)
			});
			
			if (response.ok) {
				const result = await response.json();
				console.log('保存的配置:', configData);  // 调试输出
				alert('✅ ' + result.message);
				addLog('info', '配置已保存: ' + JSON.stringify(configData.model_config, null, 2));
				
				// 重新加载配置
				await loadConfigToForm();
			} else {
				const error = await response.text();
				alert('保存失败: ' + error);
				addLog('error', '保存配置失败: ' + error);
			}
		} catch (err) {
			alert('保存失败: ' + err.message);
			addLog('error', '保存配置失败: ' + err.message);
		}
	});

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
	openConfigModal = async function() {
		await loadConfigToForm();
		originalOpenConfigModal();
	};
	
    
