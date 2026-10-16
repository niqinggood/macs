// ====================== DE Panel ======================

// Department grouping for display
var aeDepartments = [
	{ name: "⭐ 常用",       ids: ["investment-advisor", "lead-researcher", "lead-acquisition", "customer-service", "data-analyst", "ppt-polish-specialist", "bid-specialist"] },
	{ name: "📋 综合办公",   ids: ["project-assistant", "admin-assistant"] },
	{ name: "💰 投资与财务", ids: ["investment-advisor", "risk-manager", "finance-specialist"] },
	{ name: "⚖️ 法务",       ids: ["legal-review", "bid-specialist"] },
	{ name: "📝 内容与市场", ids: ["content-editor", "ppt-polish-specialist", "market-analyst", "sales-assistant", "customer-service", "social-media", "translator"] },
	{ name: "📊 数据与技术", ids: ["data-analyst", "it-ops", "product-manager", "mentor-buddy", "code-review-bot"] },
	{ name: "👥 人事",       ids: ["hr-recruiter", "trainer"] },
	{ name: "📈 获客与销售", ids: ["lead-researcher", "lead-acquisition"] },
	{ name: "🏭 运营与供应链", ids: ["supply-chain", "qa-specialist", "event-planner"] }
];
var aeShowHidden = false;
var aeViewMode = "common";
var aeCommonIDs = ["investment-advisor", "lead-researcher", "lead-acquisition", "customer-service", "data-analyst", "ppt-polish-specialist", "bid-specialist", "codex-specialist"];

function openAEContactHelp() {
	if (typeof window.openHelpContactSection === "function") {
		window.openHelpContactSection();
		return;
	}
	window.location.href = "/?help=contact#help-contact";
}

function openAEPanelLegacy() {
	document.getElementById("aePanel").classList.add("active");
	loadAEList();
}
function closeAEPanel() {
	document.getElementById("aePanel").classList.remove("active");
}

function esc(s) {
	if (!s) return "";
	return s.replace(/&/g,"&amp;").replace(/</g,"&lt;").replace(/>/g,"&gt;").replace(/"/g,"&quot;");
}

function readinessBadge(readiness) {
	var level = readiness && readiness.level ? readiness.level : "placeholder";
	var score = readiness && readiness.score != null ? readiness.score : 0;
	var missing = readiness && readiness.missing_tools && readiness.missing_tools.length ? " | missing tools: " + readiness.missing_tools.join(", ") : "";
	var color = "#64748b";
	var bg = "#f1f5f9";
	var label = "PLACEHOLDER";
	if (level === "ready") {
		color = "#047857";
		bg = "#d1fae5";
		label = "READY";
	} else if (level === "needs_review") {
		color = "#b45309";
		bg = "#fef3c7";
		label = "REVIEW";
	}
	return '<span title="readiness score '+score+esc(missing)+'" style="display:inline-block;margin-left:6px;padding:2px 6px;border-radius:999px;background:'+bg+';color:'+color+';font-size:calc(11px * var(--macs-font-scale, 1));font-weight:700;vertical-align:middle;">'+label+' '+score+'</span>';
}

function aePanelNoteBadge(note) {
	if (!note) return "";
	return ' <span style="display:inline-block;margin-left:6px;padding:2px 6px;border-radius:999px;background:#fef2f2;color:#b91c1c;border:1px solid #fecaca;font-size:calc(11px * var(--macs-font-scale, 1));font-weight:700;vertical-align:middle;">'+esc(note)+'</span>';
}

function modelRouteBadge(de) {
	var model = de && de.model ? de.model : "";
	var available = !!(de && de.model_available);
	var reason = de && de.model_reason ? de.model_reason : "";
	var provider = de && de.model_provider ? de.model_provider : "";
	var source = de && de.model_source ? de.model_source : "";
	if (!model && !reason) return "";
	var bg = available ? "#ecfdf5" : "#fef2f2";
	var color = available ? "#047857" : "#b91c1c";
	var label = model || "no route";
	var title = [provider, source, reason].filter(Boolean).join(" | ");
	return '<span title="'+esc(title)+'" style="display:inline-block;margin-top:4px;padding:2px 6px;border-radius:6px;background:'+bg+';color:'+color+';font-size:calc(11px * var(--macs-font-scale, 1));font-weight:600;max-width:180px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;vertical-align:middle;">'+esc(label)+'</span>';
}

function renderModelRoutes(routes) {
	routes = routes || [];
	if (!routes.length) return "";
	var rows = routes.map(function(r) {
		var ok = !!r.available;
		var color = ok ? "#047857" : "#64748b";
		var status = ok ? "OK" : "NO";
		return '<tr style="border-bottom:1px solid #e5e7eb;">'+
			'<td style="padding:6px 8px;color:#475569;">'+esc(r.capability||"-")+'</td>'+
			'<td style="padding:6px 8px;font-family:monospace;font-size:calc(12.1px * var(--macs-font-scale, 1));">'+esc(r.model||"-")+'</td>'+
			'<td style="padding:6px 8px;color:#64748b;">'+esc(r.provider||"-")+'</td>'+
			'<td style="padding:6px 8px;color:#64748b;">'+esc(r.source||"-")+'</td>'+
			'<td style="padding:6px 8px;color:'+color+';font-weight:700;">'+status+'</td>'+
			'<td style="padding:6px 8px;color:#64748b;max-width:320px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;" title="'+esc(r.reason||"")+'">'+esc(r.reason||"")+'</td>'+
		'</tr>';
	}).join("");
	return '<div style="margin:12px 0;padding:12px;background:#fff;border:1px solid #e5e7eb;border-radius:8px;">'+
		'<h4 style="margin:0 0 8px;font-size:calc(14.3px * var(--macs-font-scale, 1));">Model routes</h4>'+
		'<table style="width:100%;border-collapse:collapse;font-size:calc(13.2px * var(--macs-font-scale, 1));">'+
		'<thead><tr style="text-align:left;border-bottom:1px solid #cbd5e1;color:#334155;">'+
		'<th style="padding:6px 8px;">Capability</th><th style="padding:6px 8px;">Model</th><th style="padding:6px 8px;">Provider</th><th style="padding:6px 8px;">Source</th><th style="padding:6px 8px;">Status</th><th style="padding:6px 8px;">Reason</th>'+
		'</tr></thead><tbody>'+rows+'</tbody></table></div>';
}

function aeRuntimeStatus(de) {
	var status = de.status || (de.busy ? "busy" : "idle");
	if (status === "waiting_approval" || (de.waiting_approvals || 0) > 0) {
		return { text: "等待审批", color: "#f59e0b" };
	}
	if (status === "busy" || (de.active_turns || 0) > 0) {
		return { text: "执行中", color: "#f59e0b" };
	}
	if (de.session_alive) {
		return { text: "空闲 · Codex会话保活", color: "#64748b" };
	}
	return { text: "空闲", color: "#10b981" };
}

function renderAERow(de) {
	var ready = !!(de.readiness && de.readiness.level === "ready");
	var runtime = aeRuntimeStatus(de);
	var statusColor = runtime.color || (ready ? "#10b981" : "#94a3b8");
	var statusText = runtime.text;
	var tierBadge = de.tier === "commercial" ? ' <span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;">🔒</span>' : '';
	var hiddenBadge = de.hidden ? ' <span title="暂时隐藏" style="font-size:calc(11px * var(--macs-font-scale, 1));color:#64748b;background:#f1f5f9;padding:2px 5px;border-radius:999px;">隐藏</span>' : '';
	return '<tr style="border-bottom:1px solid #f0f0f0;">'+
		'<td style="padding:8px 10px;font-weight:500;">'+esc(de.name)+aePanelNoteBadge(de.note)+tierBadge+hiddenBadge+readinessBadge(de.readiness)+'<br>'+modelRouteBadge(de)+'</td>'+
		'<td style="padding:8px 10px;"><span style="display:inline-block;width:8px;height:8px;border-radius:50%;background:'+statusColor+';margin-right:6px;"></span>'+statusText+'</td>'+
		'<td style="padding:8px 10px;color:#6b7280;font-size:calc(13.2px * var(--macs-font-scale, 1));">'+esc(de.pipeline||"-")+'</td>'+
		'<td style="padding:8px 10px;">'+(de.total_tasks||0)+'</td>'+
		'<td style="padding:8px 10px;">'+(de.queued_tasks||0)+'</td>'+
		'<td style="padding:8px 10px;color:#9ca3af;font-size:calc(13.2px * var(--macs-font-scale, 1));">'+(de.last_talk_time||"-")+'</td>'+
		'<td style="padding:8px 10px;"><button onclick="openAEDetailModal(\''+esc(de.id)+'\')" style="padding:4px 10px;background:#f3f4f6;border:1px solid #e5e7eb;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));">详情</button></td>'+
		'</tr>';
}

function renderAETable(employees) {
	var rows = employees.map(function(de) { return renderAERow(de); }).join("");
	if (!rows) return '<div style="text-align:center;padding:24px;color:#9ca3af;">暂无AI员工</div>';
	return '<div style="background:#fff;border:1px solid #e5e7eb;border-radius:8px;overflow:hidden;">'+
		'<table style="width:100%;border-collapse:collapse;font-size:calc(14.3px * var(--macs-font-scale, 1));">'+
		'<thead><tr style="background:#f8fafc;border-bottom:1px solid #e5e7eb;color:#64748b;text-align:left;">'+
		'<th style="padding:8px 10px;font-weight:600;">员工</th>'+
		'<th style="padding:8px 10px;font-weight:600;">状态</th>'+
		'<th style="padding:8px 10px;font-weight:600;">流程</th>'+
		'<th style="padding:8px 10px;font-weight:600;">任务</th>'+
		'<th style="padding:8px 10px;font-weight:600;">队列</th>'+
		'<th style="padding:8px 10px;font-weight:600;">最近</th>'+
		'<th style="padding:8px 10px;font-weight:600;">操作</th>'+
		'</tr></thead><tbody>'+rows+'</tbody></table>'+
		'</div>';
}

function renderAEViewTabs() {
	function btn(mode, label) {
		var active = aeViewMode === mode;
		return '<button onclick="aeViewMode=\''+mode+'\';loadAEList()" style="padding:7px 12px;border-radius:8px;border:1px solid '+(active?'#10b981':'#cbd5e1')+';background:'+(active?'#ecfdf5':'#fff')+';color:'+(active?'#047857':'#334155')+';cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:'+(active?'700':'500')+';">'+label+'</button>';
	}
	return '<div style="display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin:0 0 12px;">'+
		btn("common", "常用")+
		btn("all", "全部员工")+
		'</div>';
}

function commonEmployees(employees) {
	var byId = {};
	employees.forEach(function(e) { byId[e.id] = e; });
	var out = [];
	for (var i = 0; i < aeCommonIDs.length; i++) {
		if (byId[aeCommonIDs[i]]) out.push(byId[aeCommonIDs[i]]);
	}
	return out;
}

async function loadAEList() {
	try {
		var includeHidden = aeShowHidden || aeViewMode === "all";
		var resp = await fetch("/api/ae/list" + (includeHidden ? "?include_hidden=1" : ""));
		var data = await resp.json();
		var employees = data.employees || [];
		var visibleEmployees = aeViewMode === "common" ? commonEmployees(employees) : employees;
		// Update count badges
		var totalBadge = document.getElementById('aeCountBadge');
		var freeBadge = document.getElementById('aeFreeBadge');
		var commercialBadge = document.getElementById('aeCommercialBadge');
		if (totalBadge) totalBadge.textContent = '共 ' + visibleEmployees.length;
		if (freeBadge) {
			var freeCount = employees.filter(function(e) { return e.tier !== 'commercial'; }).length;
			freeBadge.textContent = '免费 ' + freeCount;
		}
		if (commercialBadge) {
			var commercialCount = employees.filter(function(e) { return e.tier === 'commercial'; }).length;
			if (commercialCount > 0) {
				commercialBadge.style.display = 'inline';
				commercialBadge.textContent = '🔒 ' + commercialCount;
			} else {
				commercialBadge.style.display = 'none';
			}
		}

		var readyCount = visibleEmployees.filter(function(e) { return e.readiness && e.readiness.level === 'ready'; }).length;
		var reviewCount = visibleEmployees.filter(function(e) { return e.readiness && e.readiness.level === 'needs_review'; }).length;
		var placeholderCount = visibleEmployees.filter(function(e) { return !e.readiness || e.readiness.level === 'placeholder'; }).length;

		var html = '';
	html += renderAEViewTabs();
	html += '<div style="margin:0 0 12px;padding:10px 12px;background:#fef2f2;border:1px solid #fecaca;border-radius:8px;color:#991b1b;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;">'+
		'<span><b style="color:#dc2626;">常用 AI 员工可白嫖试用。</b> 如果发现现有员工或流程不能满足需求，可以联系定制专属 AI 员工、工作流和行业 SOP。</span>'+
		'<button type="button" onclick="openAEContactHelp()" style="padding:5px 12px;background:#dc2626;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:700;white-space:nowrap;">查看联系方式</button>'+
		'</div>';
	html += '<div style="margin-bottom:12px;display:flex;gap:8px;align-items:center;flex-wrap:wrap;">'+
		'<button onclick="openAEEditModal()" style="padding:8px 16px;background:#10b981;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;">+ 新增AI员工</button>'+
		(aeViewMode === "all" ? '<button onclick="aeShowHidden=!aeShowHidden;loadAEList()" style="padding:8px 12px;background:#f8fafc;color:#334155;border:1px solid #cbd5e1;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">'+(aeShowHidden?'隐藏已隐藏员工':'显示隐藏员工')+'</button>' : '')+
		'</div>';

	html += '<div style="margin:-4px 0 12px;display:flex;gap:8px;align-items:center;flex-wrap:wrap;">'+
		'<span style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#047857;background:#d1fae5;padding:4px 8px;border-radius:999px;">READY '+readyCount+'</span>'+
		'<span style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#b45309;background:#fef3c7;padding:4px 8px;border-radius:999px;">REVIEW '+reviewCount+'</span>'+
		'<span style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;background:#f1f5f9;padding:4px 8px;border-radius:999px;">PLACEHOLDER '+placeholderCount+'</span>'+
		'</div>';

	if (visibleEmployees.length === 0) {
			document.getElementById("aeListContent").innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">暂无AI员工</div>';
			return;
		}

		html += renderAETable(visibleEmployees);

		document.getElementById("aeListContent").innerHTML = html;
	} catch(e) {
		document.getElementById("aeListContent").innerHTML = '<div style="color:#ef4444;padding:12px;">错误: '+e.message+'</div>';
	}
}

async function loadAEDetail(id) {
	try {
		ensureAEDetailModal();
		document.getElementById('aeDetailBody').innerHTML = '<div style="text-align:center;padding:40px;color:#9ca3af;">加载中...</div>';
		var resp = await fetch("/api/ae/detail?id="+encodeURIComponent(id));
		var data = await resp.json();
		window._currentAEName = data.name || id;
		var runtime = aeRuntimeStatus(data);
		var html = '<div style="background:#f9fafb;border-radius:8px;padding:16px;margin-bottom:12px;">'+
			'<strong>'+esc(data.name||id)+'</strong>'+aePanelNoteBadge(data.note)+(data.readiness ? readinessBadge(data.readiness) : '')+
			' | @触发名: '+esc(data.at_name||id)+
			' | 状态: '+runtime.text+
			' | 工作流：'+esc(data.pipeline||"-")+
			' | 租户: '+esc(data.tenant||"-")+
			' | KB: '+(data.kb_slugs||[]).join(", ")+
			'</div>';
		html += renderModelRoutes(data.model_routes || (data.model_route ? [data.model_route] : []));
		// Pipeline execution progress bar
		if (data.pipeline_progress && data.pipeline_progress.steps && data.pipeline_progress.steps.length > 0) {
			var pp = data.pipeline_progress;
			var totalSteps = pp.total_steps || pp.steps.length;
			var statusColor = pp.status === "waiting_approval" ? "#f59e0b" : "#6366f1";
			var statusText = pp.status === "waiting_approval" ? "⏳ 等待审批" : "🔄 执行中";
			var pct = totalSteps > 0 ? Math.round((pp.current_step || 0) / totalSteps * 100) : 0;
			html += '<div style="margin:12px 0;padding:12px;background:#f8fafc;border-radius:8px;border:1px solid #e5e7eb;">'+
				'<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px;">'+
					'<span style="font-weight:600;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#1e293b;">'+esc(pp.pipeline_name)+'</span>'+
					'<span style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:'+statusColor+';font-weight:500;">'+statusText+'</span>'+
				'</div>'+
				// Progress bar
				'<div style="background:#e5e7eb;border-radius:4px;height:6px;margin-bottom:8px;">'+
					'<div style="background:'+statusColor+';border-radius:4px;height:6px;width:'+pct+'%;transition:width .3s;"></div>'+
				'</div>'+
				'<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#64748b;margin-bottom:8px;">'+pp.current_step+'/'+totalSteps+' 步骤 · '+pct+'%</div>';
			// Step list
			for (var s = 0; s < pp.steps.length; s++) {
				var step = pp.steps[s];
				var icon = step.status === "completed" ? "✅" : step.status === "running" ? "🔄" : step.status === "failed" ? "❌" : step.status === "waiting_approval" ? "⏳" : "⬜";
				var stepColor = step.status === "completed" ? "#10b981" : step.status === "running" ? "#6366f1" : step.status === "failed" ? "#ef4444" : step.status === "waiting_approval" ? "#f59e0b" : "#d1d5db";
				var dur = step.duration_ms ? ' <span style="color:#94a3b8;font-size:calc(11px * var(--macs-font-scale, 1));">('+(step.duration_ms/1000).toFixed(1)+'s)</span>' : '';
				html += '<div style="display:flex;align-items:center;gap:6px;padding:3px 0;font-size:calc(13.2px * var(--macs-font-scale, 1));color:'+stepColor+';">'+
					'<span style="width:16px;text-align:center;">'+icon+'</span>'+
					'<span>'+esc(step.name)+'</span>'+dur+
					'</div>';
			}
			html += '</div>';
		}

		if (data.work_log && data.work_log.length > 0) {
			html += '<h4 style="margin:12px 0 6px;font-size:calc(14.3px * var(--macs-font-scale, 1));">工作记录</h4>'+
				'<table style="width:100%;border-collapse:collapse;">'+
				'<thead><tr style="text-align:left;border-bottom:2px solid #e5e7eb;">'+
				'<th style="padding:8px;">时间</th><th style="padding:8px;">任务</th><th style="padding:8px;">状态</th>'+
				'</tr></thead><tbody>';
			data.work_log.forEach(function(w) {
				var onclickAttr = w.exec_id ? ' onclick="showExecInPipeline(\''+esc(w.exec_id)+'\')"' : '';
				var cursorStyle = w.exec_id ? 'cursor:pointer;' : '';
				html += '<tr'+onclickAttr+' style="'+cursorStyle+'border-bottom:1px solid #f0f0f0;">'+
					'<td style="padding:8px;">'+w.created_at+'</td>'+
					'<td style="padding:8px;max-width:400px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">'+esc(w.task)+'</td>'+
					'<td style="padding:8px;">'+w.status+'</td>'+
					'</tr>';
			});
			html += '</tbody></table>';
		}
		// Execution records for this DE
		html += '<h4 style="margin:16px 0 8px;font-size:calc(14.3px * var(--macs-font-scale, 1));">⏱ 执行记录</h4>'+
			'<div id="aeExecRecords" style="max-height:300px;overflow-y:auto;">加载中...</div>';

		html += '<div style="margin-top:12px;display:flex;gap:8px;">'+
			'<button onclick="closeAEDetailModal()" style="padding:6px 12px;background:#f3f4f6;border:1px solid #e5e7eb;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">关闭</button>'+
			'<button onclick="openAEEditModal(\''+esc(id)+'\')" style="padding:6px 12px;background:#6366f1;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">✏️ 编辑</button>'+
			'</div>';
		document.getElementById("aeDetailBody").innerHTML = html;

		// Store pipeline name for execution record filtering
		window._aeCurrentPipeline = data.pipeline || '';

		// Load execution records
		loadAEExecRecords(id, data.pipeline || '');
	} catch(e) {
		document.getElementById("aeDetailBody").innerHTML = '<div style="color:#ef4444;padding:12px;">错误: '+e.message+'</div>';
	}
}

function ensureAEDetailModal() {
	if (document.getElementById('aeDetailModal')) return;
	var overlay = document.createElement('div');
	overlay.id = 'aeDetailModal';
	overlay.className = 'ae-edit-overlay';
	overlay.innerHTML = '<div class="ae-edit-modal">'+
		'<div class="ae-edit-header">'+
			'<h3>AI员工详情</h3>'+
			'<button class="ae-edit-close" onclick="closeAEDetailModal()">&times;</button>'+
		'</div>'+
		'<div id="aeDetailBody" class="ae-edit-body">加载中...</div>'+
	'</div>';
	overlay.onclick = function(e) { if (e.target === overlay) closeAEDetailModal(); };
	document.body.appendChild(overlay);
}

function openAEDetailModal(id) {
	window._currentAEDetailId = id;
	ensureAEDetailModal();
	document.getElementById('aeDetailModal').style.display = 'flex';
	loadAEDetail(id);
}

function closeAEDetailModal() {
	var overlay = document.getElementById('aeDetailModal');
	if (overlay) overlay.style.display = 'none';
	window._currentAEDetailId = null;
}

function sortAEExecutionsDesc(execs) {
	return (execs || []).slice().sort(function(a, b) {
		return new Date(b.created_at || 0).getTime() - new Date(a.created_at || 0).getTime();
	});
}

async function loadAEExecRecords(aeId, pipelineName) {
	var el = document.getElementById('aeExecRecords');
	if (!el) return;
	try {
		var resp = await fetch('/api/pipeline/executions');
		var data = await resp.json();
		// Filter by pipeline name (execution IDs don't contain DE IDs)
		var filterName = pipelineName || window._aeCurrentPipeline || '';
		var execs = sortAEExecutionsDesc((data.executions || []).filter(function(e) {
			return filterName && e.pipeline_name === filterName;
		}));
		if (execs.length === 0) {
			el.innerHTML = '<div style="color:#9ca3af;font-size:calc(13.2px * var(--macs-font-scale, 1));">' + '暂无执行记录' + '</div>';
			return;
		}
		// Populate pipelineExecCache so showExecutionDetail can find them
		if (typeof pipelineExecCache !== 'undefined') {
			execs.forEach(function(e) { pipelineExecCache[e.id] = e; });
		}
		var icons = {running:'🔄',waiting_approval:'⏳',completed:'✅',failed:'❌',stopped:'⏹'};
		var colors = {running:'#6366f1',waiting_approval:'#f59e0b',completed:'#10b981',failed:'#ef4444',stopped:'#6b7280'};
		var html = '';
		execs.forEach(function(e) {
			var st = e.status || '?';
			var steps = e.steps || [];
			var done = steps.filter(function(s){return s.status==='completed'}).length;
			html += '<div style="padding:6px 8px;margin:4px 0;background:#f9fafb;border-radius:6px;font-size:calc(12.1px * var(--macs-font-scale, 1));cursor:pointer;display:flex;justify-content:space-between;align-items:center;" onclick="showExecInPipeline(\''+e.id+'\')">'+
				'<span>'+
				'<span style="color:'+(colors[st]||'#6b7280')+'">'+(icons[st]||'?')+'</span> '+
				'<span style="font-weight:500">'+escHtml(e.pipeline_name||aeId)+'</span> '+
				'<span style="color:#6b7280">'+done+'/'+steps.length+'步</span> '+
				'<span style="color:#9ca3af">'+formatTime(e.created_at)+'</span>'+
				'</span>'+
				'<span style="color:#6366f1;font-size:calc(11px * var(--macs-font-scale, 1));font-weight:500;flex-shrink:0;">查看 →</span>'+
				'</div>';
		});
		el.innerHTML = html;
	} catch(e) {
		el.innerHTML = '<div style="color:#ef4444;font-size:calc(12.1px * var(--macs-font-scale, 1));">加载失败</div>';
	}
}

function showExecInPipeline(execId) {
	// Open execution detail in new page
	window.open('/web/execution.html?id=' + encodeURIComponent(execId), '_blank');
}

// ====================== AE Edit Modal ======================

var currentEditAE = null;

// Create modal DOM on first use
function ensureAEEditModal() {
    if (document.getElementById('aeEditModal')) return;
    var overlay = document.createElement('div');
    overlay.id = 'aeEditModal';
    overlay.className = 'ae-edit-overlay';
    overlay.innerHTML = '<div class="ae-edit-modal">'+
        '<div class="ae-edit-header">'+
            '<h3 id="aeEditTitle">✏️ 编辑AI员工</h3>'+
            '<button class="ae-edit-close" onclick="closeAEEditModal()">&times;</button>'+
        '</div>'+
        '<div id="aeEditBody" class="ae-edit-body">加载中...</div>'+
    '</div>';
    overlay.onclick = function(e) { if (e.target === overlay) closeAEEditModal(); };
    document.body.appendChild(overlay);
}

function openAEEditModal(id) {
    currentEditAE = id;
    ensureAEEditModal();
    var overlay = document.getElementById('aeEditModal');
    overlay.style.display = 'flex';
    document.getElementById('aeEditBody').innerHTML = '<div style="text-align:center;padding:40px;color:#9ca3af;">加载中...</div>';
    loadAEEditForm(id);
}

function closeAEEditModal() {
    var overlay = document.getElementById('aeEditModal');
    if (overlay) overlay.style.display = 'none';
    currentEditAE = null;
}

function renderNewAEEditForm() {
    var html = '';
    html += '<div class="ae-edit-field">'+
        '<label>员工ID <span style="color:#94a3b8;font-weight:400;">— 唯一标识，如 my-agent</span></label>'+
        '<input id="aeEditId" placeholder="例: my-custom-agent">'+
        '</div>';
    html += '<div class="ae-edit-field">'+
        '<label>名称</label>'+
        '<input id="aeEditName" placeholder="AI员工显示名称">'+
        '</div>';
    html += '<div class="ae-edit-field">'+
        '<label>备注 <span style="color:#94a3b8;font-weight:400;">— 只用于展示，不参与 @ 触发</span></label>'+
        '<input id="aeEditNote" placeholder="例: 发现客户">'+
        '</div>';
    html += '<div class="ae-edit-field">'+
        '<label>@触发名 <span style="color:#94a3b8;font-weight:400;">— 用户用 @这个名字 触发</span></label>'+
        '<input id="aeEditAtName" placeholder="例: 投资顾问">'+
        '</div>';
    html += '<div class="ae-edit-field">'+
        '<label>角色描述</label>'+
        '<input id="aeEditRole" placeholder="例: 投资标的分析与择时建议">'+
        '</div>';
    html += '<div class="ae-edit-field">'+
        '<label>工作流 <span style="color:#94a3b8;font-weight:400;">— 绑定的流程</span></label>'+
        '<input id="aeEditPipeline" placeholder="例: investment-advisory">'+
        '</div>';
    html += '<div class="ae-edit-field">'+
        '<label>system_message <span style="color:#4b5563;font-weight:400;">— 定义AI员工的角色、边界、行为规则</span></label>'+
        '<textarea id="aeEditSystemPrompt" rows="6" placeholder="你是专业的XX，负责... 如果用户没有提供足够信息，必须先追问..."></textarea>'+
        '</div>';
    html += '<div class="ae-edit-actions">'+
        '<button onclick="closeAEEditModal()" class="ae-edit-cancel">取消</button>'+
        '<button onclick="saveNewAE()" class="ae-edit-save">创建</button>'+
        '</div>';
    document.getElementById('aeEditBody').innerHTML = html;
}

async function saveNewAE() {
    try {
        var id = document.getElementById('aeEditId').value.trim();
        if (!id) { alert('请输入员工ID'); return; }
        var payload = {
            id: id,
            name: document.getElementById('aeEditName').value.trim(),
            note: document.getElementById('aeEditNote').value.trim(),
            at_name: document.getElementById('aeEditAtName').value.trim() || id,
            role: document.getElementById('aeEditRole').value.trim(),
            pipeline: document.getElementById('aeEditPipeline').value.trim(),
            system_message: document.getElementById('aeEditSystemPrompt').value.trim(),
        };
        var resp = await fetch('/api/ae/save', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(payload)
        });
        var result = await resp.json();
        if (result.ok) {
            closeAEEditModal();
            loadAEList();
            if (typeof openAEPanel === 'function') openAEPanel();
        } else {
            alert('创建失败: ' + (result.error || '未知错误'));
        }
    } catch(e) {
        alert('创建失败: ' + e.message);
    }
}

async function loadAEEditForm(id) {
    try {
        var resp = await fetch("/api/ae/detail?id="+encodeURIComponent(id));
        var data = await resp.json();
        if (!data.name) { throw new Error("员工不存在"); }

        var examples = data.routing_examples || [];
        var html = '';

        // Kind badge + Pipeline
        var kindLabel = data.kind === "router" ? "🔀 路由模式（多角色）" : "📌 单角色";
        var kindColor = data.kind === "router" ? "#8b5cf6" : "#10b981";
        html += '<div style="margin-bottom:16px;display:flex;gap:8px;align-items:center;">'+
            '<span style="display:inline-block;padding:3px 10px;background:'+kindColor+';color:#fff;border-radius:4px;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">'+kindLabel+'</span>'+
            '<span style="font-size:calc(14.3px * var(--macs-font-scale, 1));color:#6b7280;">工作流：<strong>'+esc(data.pipeline||"-")+'</strong></span>'+
            '</div>';

        // Name
        html += '<div class="ae-edit-field">'+
            '<label>名称</label>'+
            '<input id="aeEditName" value="'+esc(data.name||"")+'" placeholder="AI员工显示名称">'+
            '</div>';

        html += '<div class="ae-edit-field">'+
            '<label>备注 <span style="color:#94a3b8;font-weight:400;">— 只用于展示，不参与 @ 触发</span></label>'+
            '<input id="aeEditNote" value="'+esc(data.note||"")+'" placeholder="例: 发现客户">'+
            '</div>';

        html += '<div class="ae-edit-field">'+
            '<label>@触发名 <span style="color:#94a3b8;font-weight:400;">— 用户用 @这个名字 触发</span></label>'+
            '<input id="aeEditAtName" value="'+esc(data.at_name||id)+'" placeholder="例: 潜客研究">'+
            '</div>';

        // Role
        html += '<div class="ae-edit-field">'+
            '<label>角色描述</label>'+
            '<input id="aeEditRole" value="'+esc(data.pipeline_role||"")+'" placeholder="例: 物流追踪/退款处理/退货指导">'+
            '</div>';

        // Pipeline
        html += '<div class="ae-edit-field">'+
            '<label>工作流 <span style="color:#94a3b8;font-weight:400;">— 绑定的流程</span></label>'+
            '<input id="aeEditPipeline" value="'+esc(data.pipeline||"")+'" placeholder="例: investment-advisory">'+
            '</div>';

        // System Prompt
        html += '<div class="ae-edit-field">'+
            '<label>system_message <span style="color:#4b5563;font-weight:400;">— 定义AI员工的角色、边界、行为规则</span></label>'+
            '<textarea id="aeEditSystemPrompt" rows="8" placeholder="你是专业的XX，负责... 如果用户没有提供足够信息，必须先追问...">'+esc(data.system_message||"")+'</textarea>'+
            '</div>';

        // Few-shot examples
        html += '<div class="ae-edit-field">'+
            '<label>Few-Shot 示例 <span style="color:#94a3b8;font-weight:400;">— 用户常见说法，帮助AI精准识别意图</span></label>'+
            '<div id="aeEditExamples" style="margin-bottom:8px;">';
        for (var i = 0; i < examples.length; i++) {
            html += renderExampleRow(i, examples[i]);
        }
        if (examples.length === 0) {
            html += '<div id="aeNoExamples" style="color:#9ca3af;font-size:calc(13.2px * var(--macs-font-scale, 1));padding:4px 0;">暂无示例，系统将自动生成</div>';
        }
        html += '</div>'+
            '<button onclick="addExampleRow()" class="ae-edit-add-btn">+ 添加示例</button>'+
            '</div>';

        // Buttons
        html += '<div class="ae-edit-actions">'+
            '<button onclick="closeAEEditModal()" class="ae-edit-cancel">取消</button>'+
            '<button onclick="saveAEEdit()" class="ae-edit-save">保存</button>'+
            '</div>';

        document.getElementById("aeEditBody").innerHTML = html;
    } catch(e) {
        document.getElementById("aeEditBody").innerHTML = '<div style="color:#ef4444;padding:24px;text-align:center;">错误: '+esc(e.message)+'<br><br><button onclick="closeAEEditModal()" class="ae-edit-cancel">关闭</button></div>';
    }
}

function renderExampleRow(idx, value) {
    return '<div style="display:flex;gap:6px;align-items:center;margin-bottom:6px;" id="exRow'+idx+'">'+
        '<input value="'+esc(value||"")+'" data-exidx="'+idx+'" style="flex:1;padding:7px 10px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(14.3px * var(--macs-font-scale, 1));" placeholder="用户常说的话，例: 我的快递到哪了">'+
        '<button onclick="removeExampleRow('+idx+')" style="padding:4px 8px;background:#fef2f2;color:#ef4444;border:1px solid #fecaca;border-radius:4px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));flex-shrink:0;">×</button>'+
        '</div>';
}


function addExampleRow() {
	var container = document.getElementById("aeEditExamples");
	var noEl = document.getElementById("aeNoExamples");
	if (noEl) noEl.remove();
	var idx = Date.now();
	var div = document.createElement("div");
	div.innerHTML = renderExampleRow(idx, "");
	container.appendChild(div.firstElementChild);
}

function removeExampleRow(idx) {
	var row = document.getElementById("exRow"+idx);
	if (row) row.remove();
	// Show "no examples" if all removed
	var container = document.getElementById("aeEditExamples");
	if (!container.querySelector("input") && !document.getElementById("aeNoExamples")) {
		var div = document.createElement("div");
		div.id = "aeNoExamples";
		div.style.cssText = "color:#9ca3af;font-size:calc(13.2px * var(--macs-font-scale, 1));padding:4px 0;";
		div.textContent = "暂无示例，系统将自动生成";
		container.appendChild(div);
	}
}

async function saveAEEdit() {
	try {
		var editedId = currentEditAE;
		// Collect form data
		var name = document.getElementById("aeEditName").value.trim();
		var note = document.getElementById("aeEditNote").value.trim();
		var atName = document.getElementById("aeEditAtName").value.trim() || currentEditAE;
		var role = document.getElementById("aeEditRole").value.trim();
		var systemPrompt = document.getElementById("aeEditSystemPrompt").value.trim();
		var pipeline = document.getElementById("aeEditPipeline").value.trim();

		// Collect examples
		var examples = [];
		var inputs = document.querySelectorAll("#aeEditExamples input");
		inputs.forEach(function(inp) {
			var v = inp.value.trim();
			if (v) examples.push(v);
		});

		// Build payload
		var payload = {
			id: currentEditAE,
			name: name,
			note: note,
			at_name: atName,
			role: role,
			pipeline: pipeline,
			system_message: systemPrompt,
			routing_examples: examples
		};

		var resp = await fetch("/api/ae/save", {
			method: "POST",
			headers: {"Content-Type": "application/json"},
			body: JSON.stringify(payload)
		});
		var result = await resp.json();
		if (result.ok) {
			closeAEEditModal();
			loadAEList();
			if (window._currentAEDetailId === editedId) {
				loadAEDetail(editedId);
			}
			if (typeof openAEPanel === 'function') openAEPanel();
		} else {
			alert("保存失败: " + (result.error || "未知错误"));
		}
	} catch(e) {
		alert("保存失败: " + e.message);
	}
}
