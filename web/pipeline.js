// ====================== 工作流 Web UI ======================

// Department grouping for AI employee display
var aeDepartments = [
	{ name: "📋 综合办公",   color: "#B91C1C", bg: "#fef2f2", ids: ["project-assistant", "admin-assistant"] },
	{ name: "💰 投资与财务", color: "#B91C1C", bg: "#fef2f2", ids: ["investment-advisor", "risk-manager", "finance-specialist"] },
	{ name: "⚖️ 法务",       color: "#B91C1C", bg: "#fef2f2", ids: ["legal-review", "bid-specialist"] },
	{ name: "📝 内容与市场", color: "#B91C1C", bg: "#fef2f2", ids: ["content-editor", "ppt-polish-specialist", "market-analyst", "sales-assistant", "customer-service", "social-media", "translator"] },
	{ name: "📈 获客与销售", color: "#B91C1C", bg: "#fef2f2", ids: ["lead-researcher", "lead-acquisition"] },
	{ name: "📊 数据与技术", color: "#B91C1C", bg: "#fef2f2", ids: ["data-analyst", "it-ops", "product-manager", "mentor-buddy", "code-review-bot"] },
	{ name: "👥 人事",       color: "#B91C1C", bg: "#fef2f2", ids: ["hr-recruiter", "trainer"] },
	{ name: "🏭 运营与供应链", color: "#B91C1C", bg: "#fef2f2", ids: ["supply-chain", "qa-specialist", "event-planner"] }
];

let pipelineActiveTab = 'list';

// SVG icon helper — Lucide-style inline, no CDN dependency
var DE_ICONS = {
    plus: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>',
    help: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 0 1 5.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>',
    briefcase: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><rect x="2" y="7" width="20" height="14" rx="2" ry="2"/><path d="M16 7V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v2"/></svg>',
    pipeline: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="16 3 21 3 21 8"/><line x1="4" y1="20" x2="21" y2="3"/><polyline points="21 16 21 21 16 21"/><line x1="15" y1="15" x2="21" y2="21"/><line x1="4" y1="4" x2="9" y2="9"/></svg>',
    clock: '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>',
    check: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="20 6 9 17 4 12"/></svg>',
    xclose: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>',
    chevronRight: '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="9 18 15 12 9 6"/></svg>',
    chevronDown: '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polyline points="6 9 12 15 18 9"/></svg>',
    edit: '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>',
    trash: '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>',
    play: '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><polygon points="5 3 19 12 5 21 5 3"/></svg>',
    fileText: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>',
    zap: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg>',
};
function deIcon(name) { return DE_ICONS[name] || ''; }
// Per-role avatar: initials + unique color palette
function aeDefaultTask(eid) {
    var map = {
        'legal-review': '请审查这份合同的风险条款，重点关注违约责任和知识产权',
        'market-analyst': '请分析目标市场的竞争格局，输出竞对分析报告',
        'project-assistant': '请根据本周git提交记录生成工作周报',
        'meeting-minutes': '请整理这份会议录音，提取关键决议和待办事项',
        'content-editor': '请根据这份文档撰写一篇微信公众号文章并制作HTML',
        'ppt-polish-specialist': '请润色这份PPT，优化结构、版式和视觉风格，并生成新的PPTX',
        'book-summary': '请归纳这本书的核心观点，生成读书笔记',
        'investigator': '请对以下人员进行背景调查',
        'bid-manager': '请根据招标要求构造竞标方案',
        'portfolio-mgr': '请分析当前持仓组合，给出优化建议',
        'wealth-planner': '请帮我做一份个人理财规划',
		'investment-advisor': '请对以下标的做金融信息核验、基本面与估值研究、短线交易情报和风险预算，并标明证据缺口',
        'bid-specialist': '请解读这份招标文件，整理响应矩阵、标书初稿和风险清单',
    };
    return map[eid] || '请帮我处理以下任务';
}
function deAvatar(eid, name) {
    var initial = (name || 'AI').charAt(0); // First Chinese character as monogram
    var map = {
        'legal-review':      {bg:'#fef2f2', fg:'#dc2626'},
        'market-analyst':    {bg:'#eff6ff', fg:'#2563eb'},
        'project-assistant': {bg:'#ecfdf5', fg:'#059669'},
        'meeting-minutes':   {bg:'#fffbeb', fg:'#d97706'},
        'wechat-writer':     {bg:'#fdf2f8', fg:'#be185d'},
        'book-summary':      {bg:'#eff6ff', fg:'#0284c7'},
        'background-check':  {bg:'#eff6ff', fg:'#0284c7'},
        'bid-proposal':      {bg:'#fff7ed', fg:'#ea580c'},
        'portfolio-mgr':     {bg:'#f0f9ff', fg:'#0284c7'},
        'wealth-planner':    {bg:'#fffbeb', fg:'#b45309'},
		'investment-advisor':{bg:'#eff6ff', fg:'#2563eb'},
        'ppt-polish-specialist':{bg:'#fdf2f8', fg:'#be185d'},
        'bid-specialist':   {bg:'#fff7ed', fg:'#ea580c'},
    };
    var a = map[eid] || {bg:'#eef2ff', fg:'#6366f1'};
    return {initial:initial, bg:a.bg, fg:a.fg};
}

function aeNoteBadge(note) {
    if (!note) return '';
    return ' <span style="display:inline-flex;align-items:center;padding:1px 6px;background:#fef2f2;color:#b91c1c;border:1px solid #fecaca;border-radius:999px;font-size:calc(11px * var(--macs-font-scale, 1));font-weight:600;white-space:nowrap;">'+escHtml(note)+'</span>';
}

function execEmployeeLabel(exec) {
    if (!exec || !exec.employee_name) return '<span style="color:#9ca3af;">-</span>';
    return escHtml(exec.employee_name) + aeNoteBadge(exec.employee_note || '');
}
let pipelinePollInterval = null;
let pipelineExecCache = {};
let pipelineListCache = null;  // 缓存工作流列表，避免重复请求
let aeWorkLogCache = null;    // 缓存AI员工数据
let detailBackToAE = false;   // 从DE面板跳转过来的标记

function sortExecutionsDesc(execs) {
    return (execs || []).slice().sort(function(a, b) {
        return new Date(b.created_at || 0).getTime() - new Date(a.created_at || 0).getTime();
    });
}

// ====================== Dynamic DOM Creation ======================

(function initPipelinePanel() {
    var overlay = document.createElement('div');
    overlay.id = 'pipelinePanel';
    overlay.className = 'pipeline-overlay';
    overlay.style.display = 'none';
    overlay.innerHTML = `
        <div class="pipeline-panel">
            <div class="pipeline-header">
                <div class="pipeline-title">
                    <span>🔀</span>
                    <h2>工作流</h2>
                </div>
                <div class="pipeline-tabs">
                    <button class="pipe-tab active" data-pipeline-tab="list" onclick="switchPipelineTab('list')">📋 <span data-i18n="pipelineTabAll">全部</span></button>
                    <button class="pipe-tab" data-pipeline-tab="help" onclick="switchPipelineTab('help')">📖 <span data-i18n="pipelineHelpTab">使用说明</span></button>
                    <button class="pipe-tab" data-pipeline-tab="executions" onclick="switchPipelineTab('executions')">⏱ <span data-i18n="pipelineTabExecutions">执行记录</span></button>
                    <button class="pipe-tab" data-pipeline-tab="pending" onclick="switchPipelineTab('pending')">⏳ <span data-i18n="pipelineTabPending">待审批</span> <span id="pendingBadge" class="pipe-badge" style="display:none">0</span></button>
                    <button class="pipe-tab" data-pipeline-tab="editor" onclick="switchPipelineTab('editor')">✏️ <span data-i18n="pipelineTabEditor">编辑器</span></button>
                </div>
                <button class="pipeline-close" onclick="closePipelinePanel()">&times;</button>
            </div>
            <div id="pipelineListView" class="pipeline-view">
                <div id="pipelineQuickRun" style="display:flex;align-items:center;gap:10px;background:linear-gradient(135deg,#f0f4ff,#e8f0fe);border:1px solid #c7d2fe;border-radius:12px;padding:12px 16px;margin-bottom:12px;">
                    <span style="font-weight:700;font-size:calc(15.4px * var(--macs-font-scale, 1));color:#1e293b;white-space:nowrap;">🚀 启动工作流</span>
                    <select id="quickRunSelect" style="width:340px;flex-shrink:0;padding:8px 12px;border:1px solid #d1d5db;border-radius:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));background:#fff;">
                        <option value="">-- 选择工作流模板 --</option>
                    </select>
                    <button onclick="quickRunPipeline()" style="padding:8px 18px;background:#6366f1;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;white-space:nowrap;">启动</button>
                </div>
                <div id="pipelineStats" class="pipe-stats"></div>
                <div id="pipelineListContent" class="pipe-list pipeline-template-grid" role="group" aria-label="工作流模板">加载中...</div>
            </div>
            <div id="pipelineExecListView" class="pipeline-view" style="display:none">
                <div id="execListContent" class="pipe-list">加载中...</div>
            </div>
            <div id="pipelinePendingView" class="pipeline-view" style="display:none">
                <div id="pendingListContent" class="pipe-list">加载中...</div>
            </div>
            <div id="pipelineHelpView" class="pipeline-view" style="display:none">
                <article class="pipeline-help" aria-labelledby="pipelineHelpTitle">
                    <section class="pipeline-help-intro">
                        <h3 id="pipelineHelpTitle" data-i18n="pipelineHelpTitle">给 AI 一份固定的办事流程</h3>
                        <p class="pipeline-help-lead" data-i18n="pipelineHelpLead">把一项工作拆成固定步骤，让 AI 按模板完成。适合经常重复、步骤明确、需要核对或审核的工作。</p>
                        <p data-i18n="pipelineHelpControl">系统按模板控制步骤顺序；可以为每一步指定可用工具、设置审批。AI 每一步的判断仍需要核对。</p>
                    </section>
                    <section aria-labelledby="pipelineHelpBenefitsTitle">
                        <h4 id="pipelineHelpBenefitsTitle" data-i18n="pipelineHelpBenefitsTitle">为什么要用工作流？</h4>
                        <div class="pipeline-help-grid">
                            <div class="pipeline-help-card"><h5 data-i18n="pipelineHelpBenefitSteps">少漏步骤</h5><p data-i18n="pipelineHelpBenefitStepsText">把核对、复查等环节写进流程，每次都按模板经过这些步骤。</p></div>
                            <div class="pipeline-help-card"><h5 data-i18n="pipelineHelpBenefitReuse">方便重复使用</h5><p data-i18n="pipelineHelpBenefitReuseText">下次做同类工作，沿用模板、换一批资料，不必重新交代整套过程。</p></div>
                            <div class="pipeline-help-card"><h5 data-i18n="pipelineHelpBenefitTrack">看得清、管得住</h5><p data-i18n="pipelineHelpBenefitTrackText">查看做到哪一步、各步结果和错误；需要审批的步骤完成后会暂停，等你确认。</p></div>
                        </div>
                    </section>
                    <section aria-labelledby="pipelineHelpExampleTitle">
                        <h4 id="pipelineHelpExampleTitle" data-i18n="pipelineHelpExampleTitle">举个例子：每周行业报告</h4>
                        <p data-i18n="pipelineHelpExampleText">你可以设计这样一条流程，每周换一批资料，再按同一套步骤完成报告：</p>
                        <ol class="pipeline-help-flow">
                            <li data-i18n="pipelineHelpFlowCollect">收集资料</li><li data-i18n="pipelineHelpFlowOrganize">整理重点</li><li data-i18n="pipelineHelpFlowVerify">核对来源</li><li data-i18n="pipelineHelpFlowWrite">写成报告</li><li data-i18n="pipelineHelpFlowReview">人工审核</li>
                        </ol>
                        <p class="pipeline-help-note" data-i18n="pipelineHelpExampleNote">这是流程设计示例。是否需要审核、是否包含分支或并行，以选中的模板为准；开启审批的步骤完成后才会暂停。</p>
                    </section>
                    <section aria-labelledby="pipelineHelpHowTitle">
                        <h4 id="pipelineHelpHowTitle" data-i18n="pipelineHelpHowTitle">第一次怎么用？</h4>
                        <ol class="pipeline-help-steps">
                            <li data-i18n="pipelineHelpHowChoose">在“全部”选择合适的模板卡片，先看步骤和审批标记。</li>
                            <li data-i18n="pipelineHelpHowInput">在输入窗口写清目标、资料位置和希望得到的结果，确认后点击“启动工作流”。选中卡片本身不会执行任务。</li>
                            <li data-i18n="pipelineHelpHowTrack">在“执行记录”查看当前步骤和每一步的输出；出现错误时，先查看原因。</li>
                            <li data-i18n="pipelineHelpHowApprove">带审批标记的步骤完成后，流程会暂停。进入“待审批”核对本步结果，选择批准继续，或停止这次流程。</li>
                            <li data-i18n="pipelineHelpHowFinish">完成后检查最终结果。下次同类工作继续用这个模板，换成新的任务说明和资料即可。</li>
                        </ol>
                        <div class="pipeline-help-actions"><button type="button" onclick="switchPipelineTab('list')" data-i18n="pipelineHelpChooseAction">选择一个工作流</button></div>
                    </section>
                    <section aria-labelledby="pipelineHelpChoiceTitle">
                        <h4 id="pipelineHelpChoiceTitle" data-i18n="pipelineHelpChoiceTitle">简单问答、Agent 和工作流，怎么选？</h4>
                        <div class="pipeline-help-grid">
                            <div class="pipeline-help-card"><h5 data-i18n="pipelineHelpChoiceChat">简单问答</h5><p data-i18n="pipelineHelpChoiceChatText">问一个问题、解释概念、改写一段文字，直接与模型对话。</p></div>
                            <div class="pipeline-help-card"><h5 data-i18n="pipelineHelpChoiceAgent">Agent 模式</h5><p data-i18n="pipelineHelpChoiceAgentText">临时交给 AI 一项工作，由它安排步骤、使用工具完成。</p></div>
                            <div class="pipeline-help-card"><h5 data-i18n="pipelineHelpChoicePipeline">工作流</h5><p data-i18n="pipelineHelpChoicePipelineText">经常重复、步骤明确、需要检查或审批，按预设模板执行。</p></div>
                        </div>
                    </section>
                    <section class="pipeline-help-custom" aria-labelledby="pipelineHelpCustomTitle">
                        <h4 id="pipelineHelpCustomTitle" data-i18n="pipelineHelpCustomTitle">已有模板不适合，怎么办？</h4>
                        <p data-i18n="pipelineHelpCustomText">打开“编辑器”，填写流程名称和说明，添加步骤、设置每步的任务，再保存。可以为步骤指定可用工具和审批要求；未指定工具时按系统权限使用工具。</p>
                        <p data-i18n="pipelineHelpCustomApproval">如果发送、发布等动作需要你提前确认，把这些动作放在审批步骤之后。</p>
                        <div class="pipeline-help-actions"><button type="button" onclick="switchPipelineTab('editor')" data-i18n="pipelineHelpCreateAction">创建自己的流程</button></div>
                    </section>
                </article>
            </div>
            <div id="pipelineEditorView" class="pipeline-view" style="display:none">
                <div style="display:grid;grid-template-columns:3fr 2fr;gap:16px;">
                    <div>
                        <h4 style="margin-bottom:8px;">工作流配置</h4>
                        <input id="pipeName" placeholder="工作流名称" style="width:100%;padding:8px;margin-bottom:8px;border:1px solid #ddd;border-radius:6px;">
                        <textarea id="pipeDesc" placeholder="描述" rows="2" style="width:100%;padding:8px;margin-bottom:8px;border:1px solid #ddd;border-radius:6px;"></textarea>
                        <div style="border:1px solid #dbeafe;background:#f8fbff;border-radius:8px;padding:10px;margin-bottom:8px;">
                            <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:6px;">
                                <div><b style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#1e3a8a;">数据库连接</b><div style="font-size:calc(11px * var(--macs-font-scale, 1));color:#64748b;">仅属于当前工作流；凭据使用环境变量，不写入工作流。</div></div>
                                <button onclick="addPipelineDBConnection()" style="padding:4px 9px;background:#2563eb;color:#fff;border:none;border-radius:5px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">+ 添加连接</button>
                            </div>
                            <div id="pipeDatabaseConnections"></div>
                        </div>
                        <button onclick="addPipeStep()" style="padding:6px 14px;background:#6366f1;color:#fff;border:none;border-radius:6px;cursor:pointer;margin-bottom:8px;">+ 添加步骤</button>
                        <div id="pipeSteps" style="max-height:350px;overflow-y:auto;"></div>
                    <div style="display:flex;gap:8px;align-items:center;margin-top:8px;">
                        <span style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;white-space:nowrap;">⏰ 定时:</span>
                        <input id="pipeSchedule" placeholder="如 30 8 * * 1-5 (工作日8:30)" style="flex:1;padding:6px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(12.1px * var(--macs-font-scale, 1));">
                        <span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#94a3b8;">分 时 日 月 周</span>
                    </div>
                    <button onclick="savePipeline()" style="width:100%;padding:10px;background:#22c55e;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(15.4px * var(--macs-font-scale, 1));font-weight:600;margin-top:8px;">保存工作流</button>
                    </div>
                    <div>
                        <h4 style="margin-bottom:8px;">已有工作流模板</h4>
                        <div id="existingPipes" style="max-height:500px;overflow-y:auto;">加载中...</div>
                    </div>
                </div>
            </div>
            <div id="pipelineDetailView" class="pipeline-view" style="display:none">
                <button class="pipe-back-btn" id="detailBackBtn" onclick="goBackFromDetail()">← 返回列表</button>
                <div id="detailContent"></div>
            </div>
            <!-- 启动工作流的输入弹窗 -->
            <div id="pipelineRunModal" class="pipeline-run-overlay" style="display:none" onclick="if(event.target===this)closeRunModal()">
                <div class="pipeline-run-dialog">
                    <div class="pipeline-run-header">
                        <h3 id="runPipeName">-</h3>
                        <button class="pipeline-close" onclick="closeRunModal()">&times;</button>
                    </div>
                    <div id="runPipeSteps" style="background:#f8fafc;border-radius:8px;padding:12px;margin-bottom:16px;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.8;"></div>
                    <div style="margin-bottom:12px;">
                        <label style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#2563eb;font-weight:600;margin-bottom:4px;display:block;">📝 输入任务描述</label>
                        <textarea id="runPipeInput" rows="3" placeholder="描述你要做的事情，工作流会按步骤执行。&#10;&#10;例如：审这份采购合同 / 分析竞品A的最新动态 / 检查本周发布内容" style="width:100%;padding:10px;border:1px solid #d1d5db;border-radius:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));resize:vertical;"></textarea>
                        <div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#94a3b8;margin-top:4px;">任务描述越具体，AI 执行越精准。可以附上文件路径、URL、或具体参数。</div>
                    </div>
                    <div style="display:flex;gap:8px;justify-content:flex-end;">
                        <button onclick="closeRunModal()" style="padding:8px 20px;background:#eff6ff;border:1px solid #e2e8f0;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">取消</button>
                        <button id="runPipeBtn" onclick="executePipeline()" style="padding:8px 20px;background:#6366f1;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;">🚀 启动工作流</button>
                    </div>
                </div>
            </div>
        </div>`;
    document.body.appendChild(overlay);
})();

// ====================== Panel Toggle ======================

function goBackFromDetail() {
    if (detailBackToAE) {
        openAEPanel();
    } else {
        switchPipelineTab('executions');
    }
}

function openPipelinePanel() {
    var panel = document.getElementById('pipelinePanel');
    if (!panel) return;
    panel.classList.remove('skill-panel-mode');
    if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(panel);
    detailBackToAE = false;
    panel.style.display = 'flex';
    // Restore header for 工作流 mode
    var titleH2 = panel.querySelector('.pipeline-title h2');
    if (titleH2) titleH2.textContent = typeof window.openKittyT === 'function' ? window.openKittyT('pipelines', '工作流') : '工作流';
    var tabs = panel.querySelector('.pipeline-tabs');
    if (tabs) tabs.style.display = '';
    switchPipelineTab('list');
}

function closePipelinePanel() {
    var panel = document.getElementById('pipelinePanel');
    if (panel) {
        panel.style.display = 'none';
        panel.classList.remove('skill-panel-mode');
        // Reset header state
        var titleH2 = panel.querySelector('.pipeline-title h2');
        if (titleH2) titleH2.textContent = typeof window.openKittyT === 'function' ? window.openKittyT('pipelines', '工作流') : '工作流';
        var tabs = panel.querySelector('.pipeline-tabs');
        if (tabs) tabs.style.display = '';
    }
    if (pipelinePollInterval) { clearInterval(pipelinePollInterval); pipelinePollInterval = null; }
}

// ====================== Tab Switching ======================

document.addEventListener('openkitty:languagechange', function() {
    var panel = document.getElementById('pipelinePanel');
    var tabs = panel && panel.querySelector('.pipeline-tabs');
    var title = panel && panel.querySelector('.pipeline-title h2');
    if (title && tabs && tabs.style.display !== 'none' && typeof window.openKittyT === 'function') {
        title.textContent = window.openKittyT('pipelines', '工作流');
    }
});

function switchPipelineTab(tab) {
    pipelineActiveTab = tab;
    var tabs = document.querySelectorAll('#pipelinePanel .pipe-tab');
    tabs.forEach(function(b) {
        var selected = b.dataset.pipelineTab === tab;
        b.classList.toggle('active', selected);
        b.setAttribute('aria-pressed', String(selected));
    });
    var views = document.querySelectorAll('#pipelinePanel .pipeline-view');
    views.forEach(function(v) { v.style.display = 'none'; });

    if (pipelinePollInterval) { clearInterval(pipelinePollInterval); pipelinePollInterval = null; }

    switch (tab) {
        case 'list':
            var lv = document.getElementById('pipelineListView'); if (lv) lv.style.display = 'block';
            loadPipelineList();
            break;
        case 'executions':
            var ev = document.getElementById('pipelineExecListView'); if (ev) ev.style.display = 'block';
            loadExecutions();
            pipelinePollInterval = setInterval(loadExecutions, 3000);
            break;
        case 'pending':
            var pv = document.getElementById('pipelinePendingView'); if (pv) pv.style.display = 'block';
            loadPendingApprovals();
            pipelinePollInterval = setInterval(loadPendingApprovals, 5000);
            break;
        case 'help':
            var hv = document.getElementById('pipelineHelpView'); if (hv) { hv.style.display = 'block'; hv.scrollTop = 0; }
            break;
    }
}

// ====================== 工作流列表 ======================

async function loadPipelineList() {
    var el = document.getElementById('pipelineListContent');
    if (!el) return;

    // 有缓存时立即渲染下拉框，让用户感觉秒开
    if (pipelineListCache) {
        populateQuickRunDropdown(pipelineListCache);
    }

    el.setAttribute('aria-busy', 'true');
    el.innerHTML = '<div class="pipeline-template-state" role="status">加载中...</div>';
    try {
        var resp = await fetch('/api/pipeline/list');
        var data = await resp.json();
        var pipelines = data.pipelines || [];
        pipelineListCache = pipelines;  // 更新缓存

        // 填充快速启动下拉框
        populateQuickRunDropdown(pipelines);

        var catCounts = {};
        pipelines.forEach(function(p) {
            var cat = categorizePipeline(p);
            catCounts[cat] = (catCounts[cat] || 0) + 1;
        });

        var statsEl = document.getElementById('pipelineStats');
        if (statsEl) {
            var catEntries = Object.entries(catCounts).sort(function(a,b){return b[1]-a[1];});
            statsEl.innerHTML =
                '<div class="pipe-stat-card"><div class="pipe-stat-num">'+pipelines.length+'</div><div class="pipe-stat-label">工作流总数</div></div>' +
                '<div class="pipe-stat-card"><div class="pipe-stat-num">'+catEntries.length+'</div><div class="pipe-stat-label">业务分类</div></div>' +
                catEntries.slice(0,3).map(function(e) {
                    return '<div class="pipe-stat-card"><div class="pipe-stat-num">'+e[1]+'</div><div class="pipe-stat-label">'+escHtml(e[0])+'</div></div>';
                }).join('');
        }

        if (pipelines.length === 0) {
            document.getElementById('pipelineQuickRun').style.display = 'none';
            el.innerHTML = '<div class="pipe-empty pipeline-template-state" style="padding:60px 20px;text-align:center;">' +
                '<div style="font-size:calc(52.8px * var(--macs-font-scale, 1));margin-bottom:16px;">🔀</div>' +
                '<div style="font-size:calc(17.6px * var(--macs-font-scale, 1));font-weight:600;color:#1e293b;margin-bottom:8px;">暂无工作流</div>' +
                '<div style="font-size:calc(14.3px * var(--macs-font-scale, 1));color:#94a3b8;margin-bottom:6px;">方式一：在 config.yaml 的 <code style="background:#eff6ff;padding:2px 6px;border-radius:4px;">pipelines</code> 段中定义 YAML</div>' +
                '<div style="font-size:calc(14.3px * var(--macs-font-scale, 1));color:#94a3b8;margin-bottom:16px;">方式二：点击下方按钮，在线创建</div>' +
                '<button onclick="switchPipelineTab(\'editor\')" style="padding:8px 20px;background:#6366f1;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">✏️ 在线创建第一个工作流</button>' +
            '</div>';
            return;
        } else {
            document.getElementById('pipelineQuickRun').style.display = 'flex';
        }

        el.innerHTML = pipelines.map(function(p) {
            var stepCount = p.steps ? p.steps.length : 0;
            var stepNames = p.steps ? p.steps.slice(0,4).map(function(s){return s.name;}).join(' → ') : '';
            var cat = categorizePipeline(p);
            var tags = [cat];
            if (p.steps) p.steps.forEach(function(s) {
                if (s.require_approval) tags.push('🔒审批');
                if (s.routing && s.routing.length > 0) tags.push('🔀分支');
                if (s.repeat) tags.push('🔄循环');
                if (s.parallel && s.parallel.length > 0) tags.push('∥并行');
            });
            var uniqueTags = [];
            tags.forEach(function(t) { if (uniqueTags.indexOf(t) < 0) uniqueTags.push(t); });

            var label = p.desc || p.description || p.name;
            return '<button type="button" class="pipe-card pipeline-template-card" data-pipeline-name="'+escHtml(p.name)+'" onclick="runPipeline(this.dataset.pipelineName)" title="'+escHtml(label)+'" aria-label="启动工作流：'+escHtml(label)+'">' +
                '<span class="pipe-card-header">' +
                    '<span class="pipe-card-name">'+escHtml(label)+'</span>' +
                    '<span class="pipe-card-id">'+escHtml(p.name)+'</span>' +
                '</span>' +
                '<span class="pipe-card-body">'+escHtml(stepNames)+(stepCount > 4 ? ' ...' : '')+'</span>' +
                '<span class="pipeline-template-tags">'+uniqueTags.map(function(t){return '<span class="pipe-tag">'+escHtml(t)+'</span>';}).join('')+'</span>' +
                '<span class="pipe-card-footer">' +
                    '<span class="pipe-card-steps">'+stepCount+' 步骤</span>' +
                    '<span class="pipeline-template-open">启动 →</span>' +
                '</span>' +
            '</button>';
        }).join('');
    } catch (e) {
        el.innerHTML = '<div class="pipe-error pipeline-template-state" role="alert">加载失败: '+escHtml(e.message)+'</div>';
    } finally {
        el.removeAttribute('aria-busy');
    }
}

// ====================== 运行工作流 ======================

var _pendingPipelineName = null;

// 根据名称/描述/步骤关键词归类工作流
function categorizePipeline(p) {
    var text = (p.name + ' ' + (p.desc||'') + ' ' + (p.steps||[]).map(function(s){return s.name;}).join(' ')).toLowerCase();
    if (/日报|周报|简报|报告|总结|汇总|资讯|新闻|热点|行业/.test(text)) return '信息采集';
    if (/邮件|邮箱|收件|发件|分拣|自动回复/.test(text)) return '邮件处理';
    if (/合同|合规|审查|审核|合规|法务|风险/.test(text)) return '合规审查';
    if (/财务|财报|报表|预算|付款|发票|采购/.test(text)) return '财务审批';
    if (/竞品|竞对|竞争|市场|分析|监控/.test(text)) return '市场分析';
    if (/发布|部署|运维|DevOps|上线|回滚/.test(text)) return '发布运维';
    if (/客户|CRM|销售|营销/.test(text)) return '客户管理';
    if (/数据|删除|安全|审计/.test(text)) return '数据安全';
    return '通用流程';
}

function populateQuickRunDropdown(pipelines) {
    var quickSel = document.getElementById('quickRunSelect');
    if (!quickSel) return;
    // 按分类分组
    var groups = {};
    pipelines.forEach(function(p) {
        var cat = categorizePipeline(p);
        if (!groups[cat]) groups[cat] = [];
        groups[cat].push(p);
    });
    var html = '<option value="">-- 选择工作流模板 ('+pipelines.length+'个) --</option>';
    var catOrder = ['信息采集','邮件处理','合规审查','财务审批','市场分析','发布运维','客户管理','数据安全','通用流程'];
    catOrder.forEach(function(cat) {
        if (!groups[cat]) return;
        html += '<optgroup label="📂 '+cat+' ('+groups[cat].length+'个)">';
        groups[cat].forEach(function(p) {
            html += '<option value="'+escHtml(p.name)+'">'+escHtml(p.desc || p.name)+'</option>';
        });
        html += '</optgroup>';
    });
    quickSel.innerHTML = html;
}

function quickRunPipeline() {
    var sel = document.getElementById('quickRunSelect');
    if (!sel || !sel.value) { alert('请先选择一个工作流模板'); return; }
    runPipeline(sel.value);
}

function runPipeline(name) {
    // 从已加载列表中找这个 pipeline 的详情
    _pendingPipelineName = name;
    // 尝试从已知数据获取详情
    fetch('/api/pipeline/list')
        .then(function(r){return r.json();})
        .then(function(data){
            var pipelines = data.pipelines || [];
            var p = null;
            for (var i=0;i<pipelines.length;i++) {
                if (pipelines[i].name === name) { p = pipelines[i]; break; }
            }
            showRunModal(p, name);
        })
        .catch(function(){
            showRunModal(null, name);
        });
}

function showRunModal(p, name) {
    var modal = document.getElementById('pipelineRunModal');
    if (!modal) return;
    document.getElementById('runPipeName').textContent = '🚀 ' + (p && p.desc ? p.desc : name);
    document.getElementById('runPipeInput').value = '';

    // 显示步骤列表
    var stepsHtml = '';
    if (p && p.steps && p.steps.length > 0) {
        stepsHtml = '<div style="font-weight:600;margin-bottom:6px;color:#2563eb;">工作流步骤：</div>';
        for (var i=0; i<p.steps.length; i++) {
            var s = p.steps[i];
            var tags = [];
            if (s.require_approval) tags.push('<span style="background:#fef3c7;color:#92400e;padding:1px 5px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">需审批</span>');
            if (s.tools && s.tools.length > 0) tags.push('<span style="background:#e0e7ff;color:#3730a3;padding:1px 5px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">🔧 '+s.tools.join(', ')+'</span>');
            stepsHtml += '<div style="padding:4px 0;border-bottom:1px solid #eff6ff;">' +
                '<span style="color:#6366f1;font-weight:600;">'+(i+1)+'.</span> ' +
                escHtml(s.name || '步骤'+(i+1)) +
                (s.prompt ? ' <span style="color:#94a3b8;font-size:calc(12.1px * var(--macs-font-scale, 1));">— '+escHtml(s.prompt.substring(0,80))+'</span>' : '') +
                ' ' + tags.join(' ') +
            '</div>';
        }
        // 输入提示：用第一个步骤的 prompt 作为参考
        var firstPrompt = '';
        if (p.steps[0] && p.steps[0].prompt) {
            firstPrompt = p.steps[0].prompt;
        }
        if (firstPrompt) {
            stepsHtml += '<div style="margin-top:8px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6366f1;">💡 提示：第一个步骤的指令是 "<b>'+escHtml(firstPrompt.substring(0,120))+'</b>"，请据此描述你的任务。</div>';
        }
    } else {
        stepsHtml = '<div style="color:#94a3b8;">暂无步骤详情</div>';
    }
    document.getElementById('runPipeSteps').innerHTML = stepsHtml;
    modal.style.display = 'flex';
    document.getElementById('runPipeInput').focus();
}

function closeRunModal() {
    document.getElementById('pipelineRunModal').style.display = 'none';
    _pendingPipelineName = null;
}

async function executePipeline() {
    var name = _pendingPipelineName;
    var input = document.getElementById('runPipeInput').value.trim();
    if (!name) { closeRunModal(); return; }
    if (!input) { alert('请先输入任务描述，说明你要做什么'); return; }

    var btn = document.getElementById('runPipeBtn');
    btn.disabled = true;
    btn.textContent = '⏳ 启动中...';

    try {
        var resp = await fetch('/api/pipeline/run', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({pipeline: name, input: input, mode: 'interactive'})
        });
        var data = await resp.json();
        if (data.error) { alert('工作流错误: '+data.error); btn.disabled = false; btn.textContent = '🚀 启动工作流'; return; }
        closeRunModal();
        switchPipelineTab('executions');
    } catch (e) {
        alert('启动失败: '+e.message);
        btn.disabled = false;
        btn.textContent = '🚀 启动工作流';
    }
}

// ====================== Executions ======================

async function loadExecutions() {
    var el = document.getElementById('execListContent');
    if (!el) return;
    try {
        var resp = await fetch('/api/pipeline/executions');
        var data = await resp.json();
        var executions = sortExecutionsDesc(data.executions || []);
        executions.forEach(function(exec) { pipelineExecCache[exec.id] = exec; });

        el.innerHTML = executions.length === 0
            ? '<div class="pipe-empty">暂无执行记录<br><small style="color:#999">启动一个工作流后这里会显示</small></div>'
            : executions.map(renderExecCard).join('');
    } catch (e) {
        el.innerHTML = '<div class="pipe-error">加载失败: '+escHtml(e.message)+'</div>';
    }
}

function renderExecCard(exec) {
    var statusIcon = {running:'🔄',pausing:'⏸',paused:'⏸',waiting_approval:'⏸',completed:'✅',failed:'❌',stopped:'⏹'};
    var statusColors = {running:'#366ef4',pausing:'#d97706',paused:'#d97706',waiting_approval:'#f59e0b',completed:'#22c55e',failed:'#dc3545',stopped:'#6b7280'};
    var icon = statusIcon[exec.status] || '❓';
    var color = statusColors[exec.status] || '#6b7280';
    var steps = exec.steps || [];
    var stepSummary = steps.map(function(s) {
        var sIcon = s.status === 'completed' ? '✓' : s.status === 'running' ? '◎' : s.status === 'waiting_approval' ? '⏸' : '○';
        return '<span style="color:'+(s.status==='failed'?'#dc3545':'#6b7280')+'">'+sIcon+' '+escHtml(s.name)+'</span>';
    }).join(' · ');
    var cur = exec.current_step !== undefined ? exec.current_step + 1 : '?';
    var employeeLine = exec.employee_name
        ? '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;margin-top:4px;">AI员工：'+execEmployeeLabel(exec)+'</div>'
        : '';

    return '<div class="pipe-exec-card" onclick="showExecutionDetail(\''+escJs(exec.id)+'\')">' +
        '<div class="pipe-exec-header">' +
            '<span class="pipe-exec-name">'+icon+' '+escHtml(exec.pipeline_name || exec.id)+'</span>' +
            '<span class="pipe-exec-status" style="color:'+color+'">'+exec.status+'</span>' +
        '</div>' +
        employeeLine +
        '<div class="pipe-exec-body">'+stepSummary+'</div>' +
        '<div class="pipe-exec-footer">' +
            '<span>步骤 '+cur+'/'+steps.length+'</span>' +
            '<span>'+formatTime(exec.created_at)+'</span>' +
        '</div>' +
    '</div>';
}

// ====================== Pending Approvals ======================

async function loadPendingApprovals() {
    var el = document.getElementById('pendingListContent');
    var badge = document.getElementById('pendingBadge');
    if (!el) return;

    var pending = [];
    for (var k in pipelineExecCache) {
        if (pipelineExecCache[k].status === 'waiting_approval') pending.push(pipelineExecCache[k]);
    }

    if (badge) {
        badge.style.display = pending.length > 0 ? 'inline' : 'none';
        badge.textContent = pending.length;
    }

    if (pending.length === 0) {
        el.innerHTML = '<div class="pipe-empty">✅ 没有待审批的执行</div>';
        return;
    }

    el.innerHTML = pending.map(function(exec) {
        var steps = exec.steps || [];
        var waitStep = null;
        for (var i=0; i<steps.length; i++) { if (steps[i].status === 'waiting_approval') { waitStep = steps[i]; break; } }
        var waitOutput = waitStep ? waitStep.output : '';
        var eid = escHtml(exec.id);

        return '<div class="pipe-approval-card">' +
            '<div class="pipe-approval-header">' +
                '<span class="pipe-approval-name">⏸ '+escHtml(exec.pipeline_name || exec.id)+'</span>' +
                '<span class="pipe-approval-step">等待审批: '+(waitStep?escHtml(waitStep.name):'?')+'</span>' +
            '</div>' +
            '<div class="pipe-approval-output">'+escHtml(truncateText(waitOutput, 500))+'</div>' +
            '<div class="pipe-approval-actions">' +
                '<button class="pipe-approve-btn" onclick="event.stopPropagation();approveExecution(\''+eid+'\',\'ok\')">✅ 批准</button>' +
                '<button class="pipe-reject-btn" onclick="event.stopPropagation();var f=prompt(\'反馈意见（留空=停止）:\');if(f!==null)approveExecution(\''+eid+'\',f)">↩ 反馈</button>' +
                '<button class="pipe-stop-btn" onclick="event.stopPropagation();stopExecution(\''+eid+'\')">⏹ 停止</button>' +
            '</div>' +
        '</div>';
    }).join('');
}

async function approveExecution(execId, feedback) {
    try {
        var resp = await fetch('/api/pipeline/approve', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({exec_id: execId, feedback: feedback || 'ok'})
        });
        var data = await resp.json();
        if (data.error) { alert('审批失败: '+data.error); return; }
        loadPendingApprovals();
    } catch(e) { alert('审批请求失败: '+e.message); }
}

async function stopExecution(execId) {
    if (!confirm('确定停止此工作流执行？')) return;
    try {
        await fetch('/api/pipeline/stop', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({exec_id: execId})
        });
        loadPendingApprovals();
    } catch(e) { alert('停止失败: '+e.message); }
}

// ====================== Execution Detail ======================

async function showExecutionDetail(execId, empName, taskDesc) {
    var exec = pipelineExecCache[execId];
    if (empName) window._aeExecEmpName = empName;
    if (taskDesc) window._aeExecTask = taskDesc;
    try {
        var resp = await fetch('/api/pipeline/status/'+encodeURIComponent(execId));
        if (resp.ok) { exec = await resp.json(); pipelineExecCache[execId] = exec; }
    } catch(e) {}

    if (!exec) { alert('执行记录未找到'); return; }

    var views = document.querySelectorAll('#pipelinePanel .pipeline-view');
    views.forEach(function(v){v.style.display='none';});
    var dv = document.getElementById('pipelineDetailView');
    if (dv) dv.style.display = 'block';
    // Show back button with correct label
    var backBtn = document.getElementById('detailBackBtn');
    if (backBtn) {
        backBtn.style.display = '';
        backBtn.textContent = detailBackToAE ? '← 返回AI员工' : '← 返回执行记录';
    }

    var steps = exec.steps || [];
    var auditLog = exec.audit_log || [];
    var artifacts = exec.artifacts || (exec.delivery && exec.delivery.artifacts) || [];
    var completedSteps = steps.filter(function(step) { return step && step.status === 'completed' && step.output; });
    var finalStep = completedSteps.length ? completedSteps[completedSteps.length - 1] : null;
    var empN = window._aeExecEmpName || exec.employee_name || '';
    var taskD = window._aeExecTask || '';
    var taskInput = exec.input || taskD || '';
    var deliveryText = exec.final_output || (finalStep ? finalStep.output : '') || (exec.status === 'failed' ? (exec.error || '') : '');
    var deliverySummary = summarizeExecutionOutput(deliveryText, 260);
    var stepSummaryList = buildExecutionStepSummary(steps);

    var exportId = 'exec_export_'+Date.now();
    var html = '<div id="'+exportId+'">' +
        '<div style="margin-bottom:16px;padding:16px 18px;background:linear-gradient(135deg,#eff6ff,#f8fafc);border:1px solid #dbeafe;border-radius:12px;">'+
            '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:700;color:#2563eb;margin-bottom:8px;text-transform:uppercase;letter-spacing:0.05em;">AI 员工执行报告</div>'+
            '<div style="display:grid;grid-template-columns:1.2fr 1fr;gap:12px;">'+
                '<div style="padding:12px 14px;background:#fff;border:1px solid #e2e8f0;border-radius:10px;">'+
                    '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:700;color:#64748b;margin-bottom:6px;">任务</div>'+
                    (empN ? '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#475569;margin-bottom:4px;">执行者 <strong>'+escHtml(empN)+'</strong></div>' : '')+
                    '<div style="font-size:calc(14.3px * var(--macs-font-scale, 1));color:#0f172a;line-height:1.6;">'+escHtml((taskInput || '').substring(0, 320) || '?')+'</div>'+
                '</div>'+
                '<div style="padding:12px 14px;background:#fff;border:1px solid #e2e8f0;border-radius:10px;">'+
                    '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:700;color:#166534;margin-bottom:6px;">最终结果</div>'+
                    '<div style="font-size:calc(14.3px * var(--macs-font-scale, 1));color:#0f172a;line-height:1.6;">'+escHtml(deliverySummary || '暂无最终结果')+'</div>'+
                '</div>'+
            '</div>'+
            (stepSummaryList.length ? '<div style="margin-top:12px;padding:12px 14px;background:#fff;border:1px solid #e2e8f0;border-radius:10px;"><div style="font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:700;color:#475569;margin-bottom:6px;">步骤摘要</div>' + stepSummaryList.map(function(item) {
                return '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#334155;line-height:1.6;margin-top:4px;"><strong>' + item.index + '.</strong> ' + escHtml(item.name) + '：' + escHtml(item.summary || '暂无摘要') + '</div>';
            }).join('') + '</div>' : '')+
            (artifacts.length ? '<div style="margin-top:12px;padding:12px 14px;background:#fff;border:1px solid #bbf7d0;border-radius:10px;">'+
                '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:700;color:#166534;margin-bottom:7px;">已验证产物 ('+artifacts.length+')</div>'+
                artifacts.map(function(a) {
                    var label = (a.path || '产物').split(/[\\/]/).pop();
                    var size = a.size ? ' · '+Math.max(1, Math.round(a.size/1024))+' KB' : '';
                    var verified = a.validated ? ' · 已验证' : '';
                    return '<a href="'+escHtml(a.download_url || '#')+'" target="_blank" rel="noopener" style="display:block;color:#047857;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.7;text-decoration:none;word-break:break-all;">📎 '+escHtml(label)+escHtml(size+verified)+'</a>';
                }).join('')+
            '</div>' : '')+
        '</div>'+
        // Section 2: Execution process (below)
        '<div class="detail-header" style="display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:8px;">' +
        '<div>'+
        '<h3>'+escHtml(exec.pipeline_name || '工作流')+'</h3>' +
        '<div class="detail-meta">' +
            '<span>ID: '+escHtml(exec.id)+'</span>' +
            '<span>状态: <b style="color:'+statusColor(exec.status)+'">'+exec.status+'</b></span>' +
            '<span>创建: '+formatTime(exec.created_at)+'</span>' +
        '</div>' +
        '</div>'+
        '<div style="display:flex;gap:6px;flex-shrink:0;">'+
        (exec.status === 'running' ? '<button onclick="pausePipelineExecution(\''+exportId+'\')" style="padding:6px 12px;background:#fff7ed;color:#c2410c;border:1px solid #fed7aa;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:600;">暂停</button>' : '')+
        (exec.status === 'paused' ? '<button onclick="resumePipelineExecution(\''+exportId+'\')" style="padding:6px 12px;background:#ecfdf5;color:#047857;border:1px solid #a7f3d0;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:600;">继续</button>' : '')+
        '<button onclick="exportExecutionPDF(\''+exportId+'\',\''+escJs(exec.pipeline_name||'执行报告')+'\')" style="padding:6px 12px;background:#dc2626;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">📄 PDF</button>'+
        '<button onclick="exportExecutionImage(\''+exportId+'\')" style="padding:6px 12px;background:#6366f1;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:500;">🖼️ 图片</button>'+
        '</div>'+
        '</div>' +
    // Pipeline steps overview at top
    (function(){
        var pipeName = exec.pipeline_name || '';
        if (!pipelineListCache || !pipeName) return '';
        var pipe = pipelineListCache.find(function(p){return p.name === pipeName;});
        if (!pipe || !pipe.steps || !pipe.steps.length) return '';
        var flow = '<div class="pipe-workflow-overview" style="margin:16px 0;padding:14px 16px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:10px;">'+
            '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:700;color:#1e293b;margin-bottom:10px;">📋 工作流程 ('+pipe.steps.length+'步)</div>'+
            '<div style="display:flex;align-items:center;flex-wrap:wrap;gap:4px;">';
        pipe.steps.forEach(function(s,si){
            var sname = s.name || s.Name || '';
            var tag = '';
            if (s.require_approval) tag = ' <span style="font-size:calc(9.9px * var(--macs-font-scale, 1));color:#d97706;">🔒</span>';
            flow += '<span style="padding:5px 10px;background:#fff;border:1px solid #e2e8f0;border-radius:6px;font-size:calc(12.1px * var(--macs-font-scale, 1));white-space:nowrap;">'+
                '<b>'+(si+1)+'.</b> '+escHtml(sname)+tag+'</span>';
            if (si < pipe.steps.length - 1) {
                flow += '<span style="color:#cbd5e1;font-size:calc(11px * var(--macs-font-scale, 1));">→</span>';
            }
        });
        flow += '</div></div>';
        return flow;
    })() +
    '<div class="detail-timeline">';

    steps.forEach(function(step, i) {
        var sc = step.status==='completed'?'#4f6ef7':step.status==='running'?'#d97706':step.status==='failed'?'#dc2626':'#94a3b8';
        var icon = step.status==='completed'?'✓':step.status==='running'?'◎':step.status==='waiting_approval'?'⏸':step.status==='failed'?'✕':'○';
        html += '<div class="timeline-step" style="border-left-color:'+sc+';">' +
            // Step name — prominent heading
            '<div style="display:flex;align-items:center;gap:8px;margin-bottom:8px;padding-bottom:6px;border-bottom:1px solid #eff6ff;">'+
                '<span style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#fff;background:linear-gradient(135deg,#4f6ef7,#7b9fc5);width:20px;height:20px;border-radius:50%;display:inline-flex;align-items:center;justify-content:center;flex-shrink:0;font-weight:700;">'+(i+1)+'</span>'+
                '<span style="font-weight:700;font-size:calc(15.4px * var(--macs-font-scale, 1));color:#1e293b;">'+escHtml(step.name)+'</span>'+
                '<span class="step-status-tag '+step.status+'">'+step.status+'</span>'+
            '</div>';

        if (step.output) {
            var stepMd = markdownToHtml(step.output);
            if (typeof makePathsClickable === 'function') stepMd = makePathsClickable(stepMd);
            html += '<div class="timeline-output" style="padding-left:28px;">'+stepMd+'</div>';
        }

        if (step.tool_calls && step.tool_calls.length > 0) {
            html += '<details class="tool-calls-detail" style="padding-left:28px;"><summary>🔍 工具调用记录 ('+step.tool_calls.length+')</summary><div class="tool-calls-list">';
            step.tool_calls.forEach(function(tc) {
                html += '<div class="tool-call-item">' +
                    '<div class="tool-call-header">' +
                        '<span class="tool-call-name">'+escHtml(tc.tool_name)+'</span>' +
                        '<span class="tool-call-time">'+tc.duration_ms+'ms</span>' +
                    '</div>' +
                    (tc.params ? '<div class="tool-call-params"><span>输入</span><pre>'+escHtml(truncateText(tc.params,150))+'</pre></div>' : '')+
                    (tc.result ? '<div class="tool-call-result"><span>输出</span><pre>'+escHtml(truncateText(tc.result,200))+'</pre></div>' : '')+
                '</div>';
            });
            html += '</div></details>';
        }

        if (step.error) html += '<div class="step-error">❌ '+escHtml(step.error)+'</div>';

        html += '</div></div>';
    });
    html += '</div>';

    // Section 3: Detailed delivery outputs for drill-down
    var deliverySteps = completedSteps;
    if (deliverySteps.length > 0) {
        html += '<div style="margin-top:16px;padding:14px 16px;background:#f0fdf4;border:1px solid #bbf7d0;border-radius:10px;">'+
            '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:700;color:#166534;margin-bottom:8px;text-transform:uppercase;letter-spacing:0.05em;">交付内容</div>';
        deliverySteps.forEach(function(s){
            html += '<div style="margin-bottom:8px;">'+
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:600;color:#15803d;margin-bottom:2px;">'+escHtml(s.name)+'</div>'+
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#374151;line-height:1.6;">'+markdownToHtml(s.output.substring(0, 500))+'</div>'+
                '</div>';
        });
        html += '</div>';
    }

    if (auditLog.length > 0) {
        html += '<div class="audit-log"><h4>📝 审批记录</h4>';
        auditLog.forEach(function(a) {
            var aIcon = a.action==='approved'?'✅':a.action==='feedback'?'↩':a.action==='stopped'?'⏹':'❓';
            html += '<div class="audit-item">' +
                '<span class="audit-icon">'+aIcon+'</span>' +
                '<span class="audit-role">'+escHtml(a.role)+'</span>' +
                '<span class="audit-action">'+a.action+'</span>' +
                (a.feedback && a.feedback !== 'ok' ? '<span class="audit-feedback">"'+escHtml(truncateText(a.feedback,200))+'"</span>' : '') +
                '<span class="audit-time">'+formatTime(a.timestamp)+'</span>' +
            '</div>';
        });
        html += '</div>';
    }

    html += '</div>'; // close exportId wrapper
    var dc = document.getElementById('detailContent');
    var pp = document.querySelector('.pipeline-panel');
    var pv = document.getElementById('pipelineDetailView');
    if (pp) { pp.style.maxHeight = '90vh'; }
    if (pv) { pv.style.overflowY = 'auto'; pv.style.flex = '1'; pv.style.maxHeight = ''; }
    if (dc) { dc.innerHTML = html; dc.style.overflowY = 'auto'; }
    window._dePV = pv;
}

// ---- Export execution detail as PDF ----
window.exportExecutionPDF = function(containerId, title) {
    var el = document.getElementById(containerId);
    if (!el) return;
    var content = el.innerHTML;
    var empN = window._aeExecEmpName || '';
    var taskD = window._aeExecTask || '';
    var win = window.open('', '_blank', 'width=900,height=700');
    win.document.write('<!DOCTYPE html><html><head><meta charset="utf-8"><title>'+title+'</title>'+
        '<style>'+
        '*{margin:0;padding:0;box-sizing:border-box}'+
        'body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei","PingFang SC",sans-serif;font-size:13px;color:#1e293b;line-height:1.6;padding:40px 48px;max-width:900px;margin:0 auto;background:#fff}'+
        '.rpt-header{text-align:center;margin-bottom:28px;padding-bottom:20px;border-bottom:2px solid #e2e8f0}'+
        '.rpt-label{font-size:11px;color:#4f6ef7;text-transform:uppercase;letter-spacing:0.08em;font-weight:600;margin-bottom:4px}'+
        '.rpt-task{font-size:18px;color:#0f172a;font-weight:700;line-height:1.4;margin-bottom:6px}'+
        '.rpt-meta{font-size:12px;color:#94a3b8}'+
        '.rpt-meta strong{color:#4f6ef7;font-weight:600}'+
        '.rpt-footer{text-align:center;font-size:11px;color:#cbd5e1;padding-top:16px;margin-top:28px;border-top:1px solid #e2e8f0}'+
        '.rpt-footer .brand{color:#4f6ef7;font-weight:600}'+
        'button{display:none!important}'+
        '.detail-header button{display:none!important}'+
        '@media print{'+
        '@page{size:A4;margin:18mm 14mm}'+
        'body{padding:0!important}'+
        '.rpt-header{break-after:avoid!important;page-break-after:avoid!important}'+
        '.detail-header{break-after:avoid!important;page-break-after:avoid!important}'+
        '.pipe-workflow-overview{break-after:avoid!important;page-break-after:avoid!important;break-inside:avoid!important;page-break-inside:avoid!important}'+
        '.detail-timeline{break-before:auto!important;page-break-before:auto!important}'+
        '.timeline-step{break-inside:avoid!important;page-break-inside:avoid!important}'+
        '.timeline-step .timeline-output{break-inside:auto!important;page-break-inside:auto!important}'+
        '.timeline-output h1,.timeline-output h2,.timeline-output h3,.timeline-output h4{break-after:avoid!important;page-break-after:avoid!important}'+
        '.timeline-output h1,.timeline-output h2,.timeline-output h3,.timeline-output h4{break-before:auto!important;page-break-before:auto!important}'+
        '.timeline-output p{orphans:3!important;widows:3!important}'+
        '.timeline-output pre,.timeline-output table,.timeline-output blockquote{break-inside:avoid!important;page-break-inside:avoid!important}'+
        '.tool-calls-detail,.tool-call-item{break-inside:avoid!important;page-break-inside:avoid!important}'+
        '.audit-log{break-before:auto!important;page-break-before:auto!important}'+
        '.audit-item{break-inside:avoid!important;page-break-inside:avoid!important}'+
        '}'+
        '</style></head><body>'+
        '<div class="rpt-header">'+
        '<div class="rpt-label">AI 员工执行报告</div>'+
        (taskD ? '<div class="rpt-task">'+taskD.substring(0,120)+'</div>' : '')+
        '<div class="rpt-meta">执行者 <strong>'+(empN||'AI员工')+'</strong> · '+fmtDate()+'</div>'+
        '</div>'+
        content +
        '<div class="rpt-footer"><span class="brand">MACS</span> | AI 生成，仅供参考</div>'+
        '</body></html>');
    win.document.close();
    setTimeout(function(){ win.print(); }, 500);
};

// ---- Export execution detail as image ----
window.exportExecutionImage = function(containerId) {
    var el = document.getElementById(containerId);
    if (!el) return;
    if (typeof html2canvas === 'undefined') {
        var s = document.createElement('script');
        s.src = 'https://html2canvas.hertzen.com/dist/html2canvas.min.js';
        s.onload = function(){ exportExecutionImage(containerId); };
        document.head.appendChild(s);
        return;
    }
    var empN = window._aeExecEmpName || '';

    // Save and remove all parent constraints so the element can fully expand
    var pv = document.getElementById('pipelineDetailView');
    var pp = document.querySelector('.pipeline-panel');
    var dc = document.getElementById('detailContent');
    var saved = {};
    if (pv) { saved.pvOverflow = pv.style.overflowY; saved.pvMaxH = pv.style.maxHeight; saved.pvFlex = pv.style.flex; pv.style.overflowY = 'visible'; pv.style.maxHeight = 'none'; pv.style.flex = 'none'; }
    if (dc) { saved.dcOverflow = dc.style.overflowY; saved.dcMaxH = dc.style.maxHeight; dc.style.overflowY = 'visible'; dc.style.maxHeight = 'none'; }
    if (pp) { saved.ppMaxH = pp.style.maxHeight; pp.style.maxHeight = 'none'; }
    if (el) { saved.elOverflow = el.style.overflow; el.style.overflow = 'visible'; }

    var toolDetails = el.querySelectorAll('.tool-calls-detail');
    var btns = el.querySelectorAll('button');
    toolDetails.forEach(function(d){ d.style.display = 'none'; });
    btns.forEach(function(b){ b.style.display = 'none'; });

    var wh = el.scrollHeight + 50;
    html2canvas(el, {scale:2,backgroundColor:'#ffffff',logging:false,height:wh,windowHeight:wh+200}).then(function(c){
        // Restore
        if (pv) { pv.style.overflowY = saved.pvOverflow; pv.style.maxHeight = saved.pvMaxH; pv.style.flex = saved.pvFlex; }
        if (dc) { dc.style.overflowY = saved.dcOverflow; dc.style.maxHeight = saved.dcMaxH; }
        if (pp) { pp.style.maxHeight = saved.ppMaxH; }
        if (el) { el.style.overflow = saved.elOverflow; }
        toolDetails.forEach(function(d){ d.style.display = ''; });
        btns.forEach(function(b){ b.style.display = ''; });
        var dt = new Date(); var ds = dt.getFullYear()+('0'+(dt.getMonth()+1)).slice(-2)+('0'+dt.getDate()).slice(-2)+'_'+('0'+dt.getHours()).slice(-2)+('0'+dt.getMinutes()).slice(-2)+('0'+dt.getSeconds()).slice(-2);
        var a = document.createElement('a');
        a.download = 'execution_'+(empN||'report')+'_'+ds+'.png';
        a.href = c.toDataURL('image/png');
        a.click();
    }).catch(function(){
        if (pv) { pv.style.overflowY = saved.pvOverflow; pv.style.maxHeight = saved.pvMaxH; pv.style.flex = saved.pvFlex; }
        if (dc) { dc.style.overflowY = saved.dcOverflow; dc.style.maxHeight = saved.dcMaxH; }
        if (pp) { pp.style.maxHeight = saved.ppMaxH; }
        if (el) { el.style.overflow = saved.elOverflow; }
        toolDetails.forEach(function(d){ d.style.display = ''; });
        btns.forEach(function(b){ b.style.display = ''; });
        alert('导出失败');
    });
};

// ====================== Helpers ======================

function statusColor(s) {
    var c = {running:'#366ef4',pausing:'#d97706',paused:'#d97706',waiting_approval:'#f59e0b',completed:'#4f6ef7',failed:'#dc3545',stopped:'#6b7280'};
    return c[s] || '#6b7280';
}

function escHtml(s) {
    if (s === null || s === undefined) return '';
    return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

function fmtDate() {
    var d = new Date();
    var pad = function(n){ return ('0'+n).slice(-2); };
    return d.getFullYear()+'-'+pad(d.getMonth()+1)+'-'+pad(d.getDate())+' '+pad(d.getHours())+':'+pad(d.getMinutes())+':'+pad(d.getSeconds());
}

function escJs(s) {
    if (s === null || s === undefined) return '';
    return String(s).replace(/\\/g,'\\\\').replace(/'/g,"\\'").replace(/\n/g,' ').replace(/\r/g,'');
}

function truncateText(s, max) {
    if (!s) return '';
    return s.length > max ? s.substring(0, max) + '...' : s;
}

function formatTime(ts) {
    if (!ts) return '';
    try { return new Date(ts).toLocaleString('zh-CN', {month:'short',day:'numeric',hour:'2-digit',minute:'2-digit',second:'2-digit'}); }
    catch(e) { return ts; }
}

function markdownToHtml(text) {
    if (!text) return '';
    try { if (typeof marked !== 'undefined') return marked.parse(text); } catch(e) {}
    return '<pre>'+escHtml(text)+'</pre>';
}

function stripMarkdownToText(text) {
    if (!text) return '';
    return String(text)
        .replace(/```[\s\S]*?```/g, function(block) {
            return block.replace(/```/g, '').replace(/\n+/g, ' ');
        })
        .replace(/!\[[^\]]*\]\([^)]+\)/g, '')
        .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
        .replace(/[#>*_`~-]/g, ' ')
        .replace(/\s+/g, ' ')
        .trim();
}

function firstParagraph(text) {
    if (!text) return '';
    var parts = String(text).split(/\n\s*\n/);
    for (var i = 0; i < parts.length; i++) {
        var part = parts[i].trim();
        if (part) return part;
    }
    return String(text).trim();
}

function summarizeExecutionOutput(text, limit) {
    var raw = firstParagraph(text || '');
    var plain = stripMarkdownToText(raw);
    if (!plain) return '';
    return plain.length > limit ? plain.substring(0, limit) + '...' : plain;
}

function buildExecutionStepSummary(steps) {
    var completed = (steps || []).filter(function(step) { return step && step.status === 'completed' && step.output; });
    return completed.slice(0, 4).map(function(step, index) {
        return {
            index: index + 1,
            name: step.name || ('步骤 ' + (index + 1)),
            summary: summarizeExecutionOutput(step.output, 120)
        };
    });
}

// ====================== Digital Employee Dashboard ======================

function openAEPanel() {
    // Restore pipeline panel scroll if expanded for detail view
    var pp = document.querySelector('.pipeline-panel');
    var pv = document.getElementById('pipelineDetailView');
    if (pp) { pp.style.maxHeight = ''; pp.style.display = ''; }
    if (pv) { pv.style.overflowY = ''; pv.style.maxHeight = ''; pv.style.flex = ''; }
    var overlay = document.getElementById('pipelinePanel');
    if (!overlay) return;
    detailBackToAE = true;
    overlay.style.display = 'flex';
    var views = overlay.querySelectorAll('.pipeline-view');
    views.forEach(function(v){v.style.display='none';});
    var detailView = document.getElementById('pipelineDetailView');
    if (!detailView) return;
    detailView.style.display = 'block';
    // Hide back button when viewing DE list
    var backBtn = document.getElementById('detailBackBtn');
    if (backBtn) backBtn.style.display = 'none';

    // Update header for DE mode
    var titleH2 = overlay.querySelector('.pipeline-title h2');
    if (titleH2) titleH2.innerHTML = 'AI员工 <span id="deStatsBar" style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;font-weight:400;margin-left:8px;"></span>';
    var tabs = overlay.querySelector('.pipeline-tabs');
    if (tabs) tabs.style.display = 'none';
    var tabBtns = overlay.querySelectorAll('.pipe-tab');
    tabBtns.forEach(function(t){t.classList.remove('active');});

        // ---- Header: add button + help icon together ----
    var html = '<div style="display:flex;align-items:center;gap:8px;margin-bottom:12px;flex-wrap:wrap;">'+
        '<button onclick="openAEEditModal()" id="deAddBtn" style="display:inline-flex;align-items:center;gap:6px;padding:8px 18px;background:#374151;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;">'+deIcon('plus')+' 新增AI员工</button>'+
        '<button onclick="openDEHelpModal()" title="工作步骤说明" style="display:inline-flex;align-items:center;justify-content:center;width:36px;height:36px;background:#eff6ff;border:1px solid #e2e8f0;border-radius:8px;cursor:pointer;color:#64748b;transition:all 0.15s;" onmouseover="this.style.background=\'#e0e7ff\';this.style.color=\'#4338ca\';" onmouseout="this.style.background=\'#eff6ff\';this.style.color=\'#64748b\';">'+deIcon('help')+'</button>'+
        '<div id="aeModelDefaultsBar" style="flex:1;min-width:520px;">加载模型默认配置...</div>'+
        '</div>';
    html += '<div style="margin:0 0 16px;padding:10px 12px;background:#fef2f2;border:1px solid #fecaca;border-radius:8px;color:#991b1b;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;">'+
        '<span><b style="color:#dc2626;">常用 AI 员工可白嫖试用。</b> 如果发现现有员工或流程不能满足需求，可以联系定制专属 AI 员工、工作流和行业 SOP。</span>'+
        '<button type="button" onclick="openAEContactHelp()" style="padding:5px 12px;background:#dc2626;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:700;white-space:nowrap;">查看联系方式</button>'+
        '</div>';

    // Populate pipeline options async
    if (pipelineListCache) {
        var sel = null; // will be set after render
    }
    html += '<div id="aeWorkbenchArea">加载AI员工数据...</div>';

    document.getElementById('detailContent').innerHTML = html;
    loadAEModelDefaultsBar();
    var tabBar = document.createElement('div');
    tabBar.style.cssText = 'display:flex;gap:6px;margin-bottom:16px;background:#f3f4f6;border-radius:8px;padding:3px;width:fit-content;';
    var btn1 = document.createElement('button'); btn1.textContent = '员工列表'; btn1.id = 'aeTabListBtn';
    btn1.style.cssText = 'padding:5px 14px;border:none;background:#fff;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:600;color:#6366f1;box-shadow:0 1px 2px rgba(0,0,0,.05);';
    btn1.onclick = function(){ switchAEView('list'); };
    var btn2 = document.createElement('button'); btn2.textContent = '执行记录'; btn2.id = 'aeTabExecsBtn';
    btn2.style.cssText = 'padding:5px 14px;border:none;background:transparent;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;';
    btn2.onclick = function(){ switchAEView('execs'); };
    tabBar.appendChild(btn1); tabBar.appendChild(btn2);
    var dc = document.getElementById('detailContent');
    if (dc) dc.insertBefore(tabBar, dc.firstChild);


    // Populate pipeline select
    (function(){
        var sel = document.getElementById('dePipeline');
        if (!sel) return;
        if (pipelineListCache) {
            pipelineListCache.forEach(function(p) {
                var opt = document.createElement('option');
                opt.value = p.name;
                opt.textContent = p.desc || p.name;
                sel.appendChild(opt);
            });
        }
    })();

    // Load data
    fetch('/api/ae/worklog')
        .then(function(r){return r.json();})
        .then(function(data){
            aeWorkLogCache = data;
            renderDEWorkbench(data);
        })
        .catch(function(e){
            document.getElementById('aeWorkbenchArea').innerHTML = '<div class="pipe-error">加载失败: '+escHtml(e.message)+'</div>';
        });
}

// ---- DE Help modal ----
window.openDEHelpModal = function() {
    var overlay = document.createElement('div');
    overlay.className = 'mac-modal-backdrop';
    overlay.style.cssText = 'position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,0.4);z-index:10000;display:flex;align-items:center;justify-content:center;';
    overlay.onclick = function(e) { if (e.target === overlay) overlay.remove(); };
    overlay.innerHTML = '<div style="background:#fff;width:500px;max-width:95vw;max-height:80vh;overflow-y:auto;padding:24px;border-radius:12px;box-shadow:0 10px 40px rgba(0,0,0,0.15);" onclick="event.stopPropagation()">'+
        '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:14px;">'+
        '<h3 style="margin:0;font-size:calc(17.6px * var(--macs-font-scale, 1));">📖 工作步骤说明</h3>'+
        '<button onclick="this.closest(\'div\').parentElement.remove()" style="background:none;border:none;font-size:calc(22px * var(--macs-font-scale, 1));cursor:pointer;color:#94a3b8;">&times;</button>'+
        '</div>'+
        '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#2563eb;line-height:1.9;">'+
        '<div style="display:flex;gap:8px;align-items:flex-start;margin-bottom:8px;"><span style="background:#6366f1;color:#fff;border-radius:50%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:calc(12.1px * var(--macs-font-scale, 1));flex-shrink:0;">1</span><span><b>触发</b>：@员工名 + 任务内容，MatchAtName 匹配</span></div>'+
        '<div style="display:flex;gap:8px;align-items:flex-start;margin-bottom:8px;"><span style="background:#6366f1;color:#fff;border-radius:50%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:calc(12.1px * var(--macs-font-scale, 1));flex-shrink:0;">2</span><span><b>排队</b>：正忙则入队列等待，空闲则立即执行</span></div>'+
        '<div style="display:flex;gap:8px;align-items:flex-start;margin-bottom:8px;"><span style="background:#6366f1;color:#fff;border-radius:50%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:calc(12.1px * var(--macs-font-scale, 1));flex-shrink:0;">3</span><span><b>注入上下文</b>：对话记忆、RAG知识库、客户信息</span></div>'+
        '<div style="display:flex;gap:8px;align-items:flex-start;margin-bottom:8px;"><span style="background:#6366f1;color:#fff;border-radius:50%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:calc(12.1px * var(--macs-font-scale, 1));flex-shrink:0;">4</span><span><b>执行</b>：pipelineExec.Run() 按步骤依次执行</span></div>'+
        '<div style="display:flex;gap:8px;align-items:flex-start;margin-bottom:8px;"><span style="background:#6366f1;color:#fff;border-radius:50%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:calc(12.1px * var(--macs-font-scale, 1));flex-shrink:0;">5</span><span><b>审批</b>：HITL步骤暂停→消息中心通知→审批人操作→继续</span></div>'+
        '<div style="display:flex;gap:8px;align-items:flex-start;margin-bottom:8px;"><span style="background:#6366f1;color:#fff;border-radius:50%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:calc(12.1px * var(--macs-font-scale, 1));flex-shrink:0;">6</span><span><b>输出</b>：结果通过通道消息或Web面板返回</span></div>'+
        '<div style="display:flex;gap:8px;align-items:flex-start;"><span style="background:#6366f1;color:#fff;border-radius:50%;width:20px;height:20px;display:flex;align-items:center;justify-content:center;font-size:calc(12.1px * var(--macs-font-scale, 1));flex-shrink:0;">7</span><span><b>完成</b>：通知 → 处理队列中下一个任务</span></div>'+
        '</div>'+
        '</div>';
    document.body.appendChild(overlay);
};

function renderDELatestOutput(employee) {
    var latest = latestTerminalAEWork(employee);
    if (!latest) return '';
    var status = latest.status || 'completed';
    var palette = status === 'completed'
        ? {bg:'#f0fdf4', border:'#bbf7d0', color:'#047857', button:'#86efac', label:'✓ 有效产出'}
        : (status === 'degraded' || status === 'unavailable')
            ? {bg:'#fffbeb', border:'#fde68a', color:'#b45309', button:'#fcd34d', label:'⚠ 降级结果（不计成功）'}
            : {bg:'#fef2f2', border:'#fecaca', color:'#b91c1c', button:'#fca5a5', label:'✕ 最近一次失败'};
    var execId = escJs(latest.exec_id || '');
    var employeeName = escJs(employee.name || employee.id || employee.employee_id || '');
    var task = escJs(latest.task || '');
    var countText = status === 'completed' ? ' · 累计成功 '+(employee.completed || 1)+' 次' : '';
    var button = latest.exec_id ? '<button onclick="event.stopPropagation();window._aeExecEmpName=\''+employeeName+'\';window._aeExecTask=\''+task+'\';showExecutionDetail(\''+execId+'\')" style="flex:0 0 auto;padding:3px 8px;background:#fff;color:'+palette.color+';border:1px solid '+palette.button+';border-radius:5px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:700;">查看详情</button>' : '';
    return '<div style="margin:0 0 8px;padding:7px 8px;background:'+palette.bg+';border:1px solid '+palette.border+';border-radius:7px;display:flex;align-items:center;justify-content:space-between;gap:8px;">'+
        '<span style="min-width:0;color:'+palette.color+';font-size:calc(12.1px * var(--macs-font-scale, 1));white-space:nowrap;overflow:hidden;text-overflow:ellipsis;" title="'+escHtml(latest.task || '')+'">'+palette.label+countText+' · '+escHtml(latest.task || '最近结果')+'</span>'+button+'</div>';
}

// Kept for reference; the AI employee panel intentionally uses the compact
// common-employee list rendered by renderDEWorkbench below.
function renderDEWorkbenchCards(data) {
    var employees = data.employees || [];
    // Update header stats
    var statsBar = document.getElementById('deStatsBar');
    if (statsBar) {
        var freeCount = employees.filter(function(e){return e.tier!=='commercial';}).length;
        var commercialCount = employees.length - freeCount;
        statsBar.textContent = '共 ' + employees.length + ' 人 · 免费 ' + freeCount + ' · 🔒 ' + commercialCount;
    }
    var area = document.getElementById('aeWorkbenchArea');
    if (!area) return;
    var html = '';
    if (employees.length === 0) {
        html += '<div class="pipe-empty">暂无AI员工<br><small>点击上方「+ 新增AI员工」创建，数据保存到数据库</small></div>';
        area.innerHTML = html;
        return;
    }
    // --- Department-grouped display ---
    var depById = {};
    employees.forEach(function(e) { depById[e.id||e.employee_id] = e; });
    var depRendered = {};

    for (var di = 0; di < aeDepartments.length; di++) {
        var dept = aeDepartments[di];
        var deptEmps = [];
        for (var dj = 0; dj < dept.ids.length; dj++) {
            var _e = depById[dept.ids[dj]];
            if (!_e) continue;
            deptEmps.push(_e);
            depRendered[dept.ids[dj]] = true;
        }
        if (deptEmps.length === 0) continue;

                        // Department header — colored name + underline
        var deptBg = dept.bg || '#f8fafc';
        var deptColor = dept.color || '#991b1b';
        html += '<div style="margin:0 0 16px 0;">'+
            '<div style="display:flex;align-items:baseline;gap:8px;margin-bottom:6px;">'+
                '<span style="font-size:calc(18.7px * var(--macs-font-scale, 1));font-weight:700;color:'+deptColor+';">'+dept.name+'</span>'+
                '<span style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#94a3b8;">'+deptEmps.length+'人</span>'+
            '</div>'+
            '<div style="height:2px;background:linear-gradient(90deg,'+deptColor+',transparent);border-radius:1px;margin-bottom:6px;"></div>'+
            '<div style="display:flex;flex-wrap:wrap;gap:4px;margin-top:6px;">';
        deptEmps.forEach(function(e){
            var mentionName = e.at_name || e.name || e.id || e.employee_id || '';
            html += '<span onclick="var p=document.getElementById(\'pipelinePanel\');if(p)p.style.display=\'none\';var inp=document.getElementById(\'messageInput\');if(inp){inp.value=\'@'+escJs(mentionName)+' \';inp.focus();}" style="display:inline-flex;align-items:center;gap:4px;padding:4px 10px;background:#fff;border:1px solid #e2e8f0;border-radius:16px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#1d4ed8;white-space:nowrap;transition:all 0.15s;" onmouseover="this.style.background=\'"+deptColor+"\';this.style.borderColor=\'"+deptColor+"\';this.style.color=\'#fff\';" onmouseout="this.style.background=\'#fff\';this.style.borderColor=\'#e2e8f0\';this.style.color=\'#1d4ed8\';">'+
                (function(){var av=deAvatar(e.id||e.employee_id, e.name);return '<span style="width:20px;height:20px;border-radius:50%;background:'+av.bg+';display:inline-flex;align-items:center;justify-content:center;color:'+av.fg+';font-weight:700;font-size:calc(11px * var(--macs-font-scale, 1));">'+av.initial+'</span>';})()+
                escHtml(e.name)+aeNoteBadge(e.note)+'</span>';
        });
        html += '</div></div>';


        // Department card grid
        html += '<div style="display:grid;grid-template-columns:repeat(3,1fr);gap:10px;margin-bottom:20px;">';
        deptEmps.forEach(function(e){

                var eid = escHtml(e.employee_id||e.id);
                var isBusy = e.running > 0;
                var dotColor = isBusy ? '#f59e0b' : '#22c55e';
                var dotAnim = isBusy ? 'animation:pulse 2s infinite;' : '';
                html += '<div class="pipe-exec-card'+(isBusy?' busy':'')+'" style="">' +
                    // Header: avatar + name/role
                    '<div style="display:flex;align-items:center;gap:10px;margin-bottom:10px;">'+
                        (function(){var av=deAvatar(e.id||e.employee_id, e.name);return '<div style="width:38px;height:38px;border-radius:50%;background:'+av.bg+';display:flex;align-items:center;justify-content:center;flex-shrink:0;color:'+av.fg+';font-weight:700;font-size:calc(17.6px * var(--macs-font-scale, 1));">'+av.initial+'</div>';})()+
                        '<div style="flex:1;min-width:0;">'+
                            '<div style="font-weight:600;font-size:calc(15.4px * var(--macs-font-scale, 1));color:#1e293b;">'+escHtml(e.name)+aeNoteBadge(e.note)+(e.tier==='commercial'?' <span style="background:#fef3c7;color:#92400e;padding:1px 6px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));font-weight:500;">需商业版</span>':'')+'</div>'+
                            '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#94a3b8;">'+escHtml(e.role)+' · '+escHtml(e.pipeline||'direct')+'</div>'+
                        '</div>'+
                        (isBusy?'<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#d97706;background:#fef3c7;padding:2px 6px;border-radius:4px;">忙碌</span>':'')+
                    '</div>'+
                    // Pipeline steps — always visible
                    '<div style="margin-bottom:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;line-height:1.6;">'+
                    (function(){
                        if (!pipelineListCache || !e.pipeline) return '';
                        var pipe = pipelineListCache.find(function(p){return p.name === e.pipeline;});
                        if (!pipe || !pipe.steps || !pipe.steps.length) return '';
                        return pipe.steps.map(function(s,si){return '<span>'+(si+1)+'. '+escHtml(s.name||s.Name||'')+'</span>';}).join(' → ');
                    })() +
                    '</div>'+
                    renderDELatestOutput(e)+
                    // Work history
                    '<div class="de-worklog-toggle" onclick="var d=document.getElementById(\'deWork_'+eid+'\');var t=this;if(d.style.display===\'none\'){d.style.display=\'block\';t.classList.add(\'open\');}else{d.style.display=\'none\';t.classList.remove(\'open\');}">'+
                        '执行记录 <span class="de-arrow2" style="font-size:calc(11px * var(--macs-font-scale, 1));">&#9654;</span> <span style="color:#94a3b8;font-size:calc(12.1px * var(--macs-font-scale, 1));">'+e.total_tasks+'条</span>'+
                    '</div>'+
                    '<div id="deWork_'+eid+'" style="display:none;max-height:180px;overflow-y:auto;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.5;">' +
                        (e.recent_work && e.recent_work.length > 0 ?
                            e.recent_work.map(function(w){
                                var sc = w.status==='completed'?'#059669':w.status==='failed'?'#dc2626':'#d97706';
                                var safeExecId = escJs(w.exec_id||''); var safeEmp = escJs(e.name||''); var safeTask2 = escJs(w.task||''); var execLink = w.exec_id ? ' <a href="javascript:void(0)" onclick="event.stopPropagation();window._aeExecEmpName=\''+safeEmp+'\';window._aeExecTask=\''+safeTask2+'\';showExecutionDetail(\''+safeExecId+'\')" style="color:#6366f1;">详情 →</a>' : '';
                                return '<div style="padding:3px 0;border-bottom:1px solid #eff6ff;">'+
                                    '<span style="color:'+sc+';font-weight:500;">'+escHtml(w.status)+'</span> '+
                                    escHtml((w.task||'').substring(0,40))+execLink+'</div>';
                            }).join('')
                        : '<div style="color:#94a3b8;padding:4px 0;">暂无记录 · @'+escHtml(e.at_name||e.name)+' 触发</div>') +
                    '</div>'+
                    // Bottom bar
                    '<div style="margin-top:8px;display:flex;justify-content:flex-end;gap:4px;">'+
                        '<button onclick="event.stopPropagation();deleteDE(\''+eid+'\')" style="font-size:calc(12.1px * var(--macs-font-scale, 1));padding:3px 10px;background:#fff;color:#94a3b8;border:1px solid #e2e8f0;border-radius:4px;cursor:pointer;">删除</button>'+
                        '<button onclick="event.stopPropagation();openAEEditModal(\''+eid+'\')" style="font-size:calc(12.1px * var(--macs-font-scale, 1));padding:3px 10px;background:#eff6ff;color:#64748b;border:1px solid #e2e8f0;border-radius:4px;cursor:pointer;">编辑</button>'+
                        (e.tier==='commercial'
                            ? '<span style="font-size:calc(11px * var(--macs-font-scale, 1));padding:2px 10px;background:transparent;color:#94a3b8;border:1px dashed #d1d5db;border-radius:4px;">🔒 商业版</span>'
                            : '<button onclick="event.stopPropagation();var p=document.getElementById(\'pipelinePanel\');if(p)p.style.display=\'none\';var inp=document.getElementById(\'messageInput\');if(inp){inp.value=\'@'+escJs(e.at_name||e.name)+' '+aeDefaultTask(e.id||e.employee_id||'')+'\';inp.focus();}" style="font-size:calc(11px * var(--macs-font-scale, 1));padding:2px 10px;background:transparent;color:#6366f1;border:1px solid #d1d5db;border-radius:4px;cursor:pointer;font-weight:500;">▶ 运行</button>'
                        )+
                    '</div>' +
                '</div>';

        });
        html += '</div>';
    }

    // Ungrouped / orphan employees
    var depOrphans = [];
    employees.forEach(function(e) {
        var eid = e.id || e.employee_id || '';
        if (!depRendered[eid]) depOrphans.push(e);
    });
    if (depOrphans.length > 0) {
        html += '<div style="margin:0 0 8px 0;padding:8px 12px;background:#f8fafc;border:1px solid #d1d5db;border-radius:8px;border-left:3px solid #9ca3af;">'+
            '<span style="font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;color:#6b7280;">📦 其他</span>'+
            '<span style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#94a3b8;margin-left:6px;">'+depOrphans.length+'人</span>'+
            '</div>';
        html += '<div style="display:grid;grid-template-columns:repeat(3,1fr);gap:10px;margin-bottom:16px;">';
        depOrphans.forEach(function(e){

                var eid = escHtml(e.employee_id||e.id);
                var isBusy = e.running > 0;
                var dotColor = isBusy ? '#f59e0b' : '#22c55e';
                var dotAnim = isBusy ? 'animation:pulse 2s infinite;' : '';
                html += '<div class="pipe-exec-card'+(isBusy?' busy':'')+'">' +
                    // Header: avatar + name/role
                    '<div style="display:flex;align-items:center;gap:10px;margin-bottom:10px;">'+
                        (function(){var av=deAvatar(e.id||e.employee_id, e.name);return '<div style="width:38px;height:38px;border-radius:50%;background:'+av.bg+';display:flex;align-items:center;justify-content:center;flex-shrink:0;color:'+av.fg+';font-weight:700;font-size:calc(17.6px * var(--macs-font-scale, 1));">'+av.initial+'</div>';})()+
                        '<div style="flex:1;min-width:0;">'+
                            '<div style="font-weight:600;font-size:calc(15.4px * var(--macs-font-scale, 1));color:#1e293b;">'+escHtml(e.name)+aeNoteBadge(e.note)+(e.tier==='commercial'?' <span style="background:#fef3c7;color:#92400e;padding:1px 6px;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));font-weight:500;">需商业版</span>':'')+'</div>'+
                            '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#94a3b8;">'+escHtml(e.role)+' · '+escHtml(e.pipeline||'direct')+'</div>'+
                        '</div>'+
                        (isBusy?'<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#d97706;background:#fef3c7;padding:2px 6px;border-radius:4px;">忙碌</span>':'')+
                    '</div>'+
                    // Pipeline steps — always visible
                    '<div style="margin-bottom:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;line-height:1.6;">'+
                    (function(){
                        if (!pipelineListCache || !e.pipeline) return '';
                        var pipe = pipelineListCache.find(function(p){return p.name === e.pipeline;});
                        if (!pipe || !pipe.steps || !pipe.steps.length) return '';
                        return pipe.steps.map(function(s,si){return '<span>'+(si+1)+'. '+escHtml(s.name||s.Name||'')+'</span>';}).join(' → ');
                    })() +
                    '</div>'+
                    renderDELatestOutput(e)+
                    // Work history
                    '<div class="de-worklog-toggle" onclick="var d=document.getElementById(\'deWork_'+eid+'\');var t=this;if(d.style.display===\'none\'){d.style.display=\'block\';t.classList.add(\'open\');}else{d.style.display=\'none\';t.classList.remove(\'open\');}">'+
                        '执行记录 <span class="de-arrow2" style="font-size:calc(11px * var(--macs-font-scale, 1));">&#9654;</span> <span style="color:#94a3b8;font-size:calc(12.1px * var(--macs-font-scale, 1));">'+e.total_tasks+'条</span>'+
                    '</div>'+
                    '<div id="deWork_'+eid+'" style="display:none;max-height:180px;overflow-y:auto;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.5;">' +
                        (e.recent_work && e.recent_work.length > 0 ?
                            e.recent_work.map(function(w){
                                var sc = w.status==='completed'?'#059669':w.status==='failed'?'#dc2626':'#d97706';
                                var safeExecId = escJs(w.exec_id||''); var safeEmp = escJs(e.name||''); var safeTask2 = escJs(w.task||''); var execLink = w.exec_id ? ' <a href="javascript:void(0)" onclick="event.stopPropagation();window._aeExecEmpName=\''+safeEmp+'\';window._aeExecTask=\''+safeTask2+'\';showExecutionDetail(\''+safeExecId+'\')" style="color:#6366f1;">详情 →</a>' : '';
                                return '<div style="padding:3px 0;border-bottom:1px solid #eff6ff;">'+
                                    '<span style="color:'+sc+';font-weight:500;">'+escHtml(w.status)+'</span> '+
                                    escHtml((w.task||'').substring(0,40))+execLink+'</div>';
                            }).join('')
                        : '<div style="color:#94a3b8;padding:4px 0;">暂无记录 · @'+escHtml(e.at_name||e.name)+' 触发</div>') +
                    '</div>'+
                    // Bottom bar
                    '<div style="margin-top:8px;display:flex;justify-content:flex-end;gap:4px;">'+
                        '<button onclick="event.stopPropagation();deleteDE(\''+eid+'\')" style="font-size:calc(12.1px * var(--macs-font-scale, 1));padding:3px 10px;background:#fff;color:#94a3b8;border:1px solid #e2e8f0;border-radius:4px;cursor:pointer;">删除</button>'+
                        '<button onclick="event.stopPropagation();openAEEditModal(\''+eid+'\')" style="font-size:calc(12.1px * var(--macs-font-scale, 1));padding:3px 10px;background:#eff6ff;color:#64748b;border:1px solid #e2e8f0;border-radius:4px;cursor:pointer;">编辑</button>'+
                        (e.tier==='commercial'
                            ? '<span style="font-size:calc(11px * var(--macs-font-scale, 1));padding:2px 10px;background:transparent;color:#94a3b8;border:1px dashed #d1d5db;border-radius:4px;">🔒 商业版</span>'
                            : '<button onclick="event.stopPropagation();var p=document.getElementById(\'pipelinePanel\');if(p)p.style.display=\'none\';var inp=document.getElementById(\'messageInput\');if(inp){inp.value=\'@'+escJs(e.at_name||e.name)+' '+aeDefaultTask(e.id||e.employee_id||'')+'\';inp.focus();}" style="font-size:calc(11px * var(--macs-font-scale, 1));padding:2px 10px;background:transparent;color:#6366f1;border:1px solid #d1d5db;border-radius:4px;cursor:pointer;font-weight:500;">▶ 运行</button>'
                        )+
                    '</div>' +
                '</div>';

        });
        html += '</div>';
    }
    html += '</div>';
    area.innerHTML = html;
}

var aeCommonEmployeeIDs = ["investment-advisor", "lead-researcher", "lead-acquisition", "customer-service", "data-analyst", "ppt-polish-specialist", "bid-specialist", "codex-specialist"];

var aeModelDefaultCapabilities = [
    {key:"text-to-text", label:"文生文"},
    {key:"multi-modal", label:"图识别"},
    {key:"text-to-image", label:"生图"}
];

function aeModelSourceLabel(source) {
    var map = {
        "ai_employee_default": "AI员工默认",
        "ai_employee_default_text": "AI员工默认(default)",
        "model_binding": "员工绑定",
        "model_binding_default": "员工绑定(default)",
        "provider_preference": "Provider优先级",
        "default_provider": "默认Provider",
        "none": "未配置"
    };
    return map[source] || (source || "未知");
}

async function loadAEModelDefaultsBar() {
    var bar = document.getElementById("aeModelDefaultsBar");
    if (!bar) return;
    bar.innerHTML = '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;">加载模型默认配置...</div>';
    try {
        var resp = await fetch("/api/ae/model-defaults");
        var data = await resp.json();
        if (!data.ok) throw new Error(data.error || "load failed");
        renderAEModelDefaultsBar(data);
    } catch(e) {
        bar.innerHTML = '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#b91c1c;background:#fef2f2;border:1px solid #fecaca;border-radius:8px;padding:7px 10px;">模型默认配置加载失败: '+escHtml(e.message)+'</div>';
    }
}

function renderAEModelDefaultsBar(data) {
    var bar = document.getElementById("aeModelDefaultsBar");
    if (!bar) return;
    var routes = data.routes || [];
    var options = data.options || {};
    var binding = data.model_binding || {};
    var configPath = data.config_path || "openkitty-config.yaml";
    var routeByCap = {};
    routes.forEach(function(route) { routeByCap[route.capability] = route; });

    var html = '<div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;padding:6px 8px;">'+
        '<span style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:700;color:#334155;">模型默认</span>';
    aeModelDefaultCapabilities.forEach(function(cap) {
        var route = routeByCap[cap.key] || {};
        var selected = binding[cap.key] || "";
        var capOptions = (options[cap.key] || []).slice();
        var currentModel = route.model || "";
        if (currentModel && !capOptions.some(function(opt) { return opt.model === currentModel; })) {
            capOptions.unshift({model:currentModel, provider:route.provider || "", provider_label:route.provider_label || "", available:!!route.available, reason:route.reason || ""});
        }
        var source = aeModelSourceLabel(route.source);
        var color = route.available ? "#047857" : "#b91c1c";
        html += '<label style="display:flex;align-items:center;gap:5px;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#334155;">'+
            '<span style="white-space:nowrap;">'+cap.label+'</span>'+
            '<select data-ae-model-cap="'+cap.key+'" style="max-width:170px;padding:4px 7px;border:1px solid #cbd5e1;border-radius:6px;background:#fff;font-size:calc(13.2px * var(--macs-font-scale, 1));">'+
                '<option value=""'+(selected === "" ? ' selected' : '')+'>自动</option>'+
                capOptions.map(function(opt) {
                    var provider = opt.provider_label || opt.provider || "";
                    var text = opt.model + (provider ? " / " + provider : "");
                    if (opt.available === false) text += " / 未就绪";
                    return '<option value="'+escHtml(opt.model || "")+'"'+(opt.model === selected ? ' selected' : '')+'>'+escHtml(text)+'</option>';
                }).join('')+
            '</select>'+
            '<span title="'+escHtml(route.reason || "")+'" style="color:'+color+';font-size:calc(12.1px * var(--macs-font-scale, 1));white-space:nowrap;">当前: '+escHtml(currentModel || "无")+' / '+escHtml(source)+'</span>'+
        '</label>';
    });
    html += '<button id="aeSaveModelDefaultsBtn" style="padding:4px 10px;background:#111827;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">保存</button>'+
        '<button onclick="openSupplierPanel()" title="配置下拉选项来源：Provider、模型名、能力类型、API Key" style="padding:4px 10px;background:#fff;color:#334155;border:1px solid #cbd5e1;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">配置模型来源</button>'+
        '<span title="'+escHtml(configPath)+'" style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#64748b;white-space:normal;overflow-wrap:anywhere;word-break:break-all;line-height:1.35;">配置: '+escHtml(configPath)+'</span>'+
        '</div>';
    bar.innerHTML = html;
    var saveBtn = document.getElementById("aeSaveModelDefaultsBtn");
    if (saveBtn) saveBtn.onclick = saveAEModelDefaults;
}

async function saveAEModelDefaults() {
    var btn = document.getElementById("aeSaveModelDefaultsBtn");
    var binding = {};
    aeModelDefaultCapabilities.forEach(function(cap) {
        var select = document.querySelector('select[data-ae-model-cap="'+cap.key+'"]');
        if (select && select.value) binding[cap.key] = select.value;
    });
    if (btn) {
        btn.disabled = true;
        btn.textContent = "保存中";
    }
    try {
        var resp = await fetch("/api/ae/model-defaults", {
            method: "POST",
            headers: {"Content-Type": "application/json"},
            body: JSON.stringify({model_binding: binding})
        });
        var data = await resp.json();
        if (!data.ok) throw new Error(data.error || "save failed");
        renderAEModelDefaultsBar(data);
    } catch(e) {
        alert("保存模型默认配置失败: " + e.message);
        if (btn) {
            btn.disabled = false;
            btn.textContent = "保存";
        }
    }
}

function orderAEWorkRecordsNewest(records) {
    return (records || []).slice().sort(function(a, b) {
        var bt = new Date((b && (b.updated_at || b.created_at)) || 0).getTime();
        var at = new Date((a && (a.updated_at || a.created_at)) || 0).getTime();
        return bt - at;
    });
}

function orderCommonDEEmployees(employees) {
    var byId = {};
    (employees || []).forEach(function(e) {
        byId[e.id || e.employee_id] = e;
    });
    var ordered = [];
    aeCommonEmployeeIDs.forEach(function(id) {
        if (byId[id]) ordered.push(byId[id]);
    });
    return ordered;
}

function latestSuccessfulAEWork(employee) {
    if (employee && employee.latest_success && employee.latest_success.status === 'completed') {
        return employee.latest_success;
    }
    var records = orderAEWorkRecordsNewest(employee && employee.recent_work);
    for (var i = 0; i < records.length; i++) {
        if (records[i] && records[i].status === 'completed') return records[i];
    }
    return null;
}

function latestTerminalAEWork(employee) {
    var records = orderAEWorkRecordsNewest(employee && employee.recent_work);
    for (var i = 0; i < records.length; i++) {
        var status = records[i] && records[i].status;
        if (status === 'completed' || status === 'degraded' || status === 'unavailable' || status === 'failed' || status === 'stopped') {
            return records[i];
        }
    }
    return latestSuccessfulAEWork(employee);
}

function aeEmployeeStatusText(e) {
    return (e.running || e.queued_tasks || 0) > 0 ? "忙碌" : "空闲";
}

function aeEmployeeStatusColor(e) {
    return aeEmployeeStatusText(e) === "忙碌" ? "#d97706" : "#059669";
}

function renderDEWorkbench(data) {
    var allEmployees = data.employees || [];
    var employees = orderCommonDEEmployees(allEmployees);
    var statsBar = document.getElementById('deStatsBar');
    if (statsBar) {
        statsBar.textContent = '常用 ' + employees.length + ' 人';
    }

    var area = document.getElementById('aeWorkbenchArea');
    if (!area) return;
    window._aeWorkbenchEmployeesById = {};
    allEmployees.forEach(function(e) {
        window._aeWorkbenchEmployeesById[e.id || e.employee_id] = e;
    });

    if (employees.length === 0) {
        area.innerHTML = '<div class="pipe-empty">暂无常用AI员工</div>';
        return;
    }

    var rows = employees.map(function(e) {
        var id = e.id || e.employee_id || "";
        var av = deAvatar(id, e.name);
        var status = aeEmployeeStatusText(e);
        var statusColor = aeEmployeeStatusColor(e);
        var lastTask = (e.recent_work && e.recent_work.length > 0) ? e.recent_work[0].task : "";
        return '<tr style="border-bottom:1px solid #eef2f7;">'+
            '<td style="padding:10px 12px;">'+
                '<div style="display:flex;align-items:center;gap:10px;min-width:0;">'+
                    '<div style="width:32px;height:32px;border-radius:50%;background:'+av.bg+';display:flex;align-items:center;justify-content:center;color:'+av.fg+';font-weight:700;font-size:calc(15.4px * var(--macs-font-scale, 1));flex:0 0 auto;">'+escHtml(av.initial)+'</div>'+
                    '<div style="min-width:0;">'+
                        '<div style="font-weight:650;color:#0f172a;font-size:calc(14.3px * var(--macs-font-scale, 1));">'+escHtml(e.name || id)+aeNoteBadge(e.note)+'</div>'+
                        '<div style="color:#64748b;font-size:calc(13.2px * var(--macs-font-scale, 1));white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:320px;">'+escHtml(e.role || "")+'</div>'+
                    '</div>'+
                '</div>'+
            '</td>'+
            '<td style="padding:10px 12px;color:'+statusColor+';font-weight:600;font-size:calc(13.2px * var(--macs-font-scale, 1));">'+status+'</td>'+
            '<td style="padding:10px 12px;color:#475569;font-size:calc(13.2px * var(--macs-font-scale, 1));">'+escHtml(e.pipeline || "direct")+'</td>'+
            '<td style="padding:10px 12px;color:#475569;font-size:calc(13.2px * var(--macs-font-scale, 1));">'+(e.total_tasks || 0)+'</td>'+
            '<td style="padding:10px 12px;color:#64748b;font-size:calc(13.2px * var(--macs-font-scale, 1));max-width:280px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;" title="'+escHtml(lastTask || "")+'">'+escHtml(lastTask || "-")+'</td>'+
            '<td style="padding:10px 12px;text-align:right;white-space:nowrap;">'+
                '<button data-ae-action="detail" data-ae-id="'+escHtml(id)+'" style="padding:4px 10px;border:1px solid #cbd5e1;background:#fff;color:#334155;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">详情</button> '+
                '<button data-ae-action="run" data-ae-id="'+escHtml(id)+'" style="padding:4px 10px;border:1px solid #d1d5db;background:#f8fafc;color:#4f46e5;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">运行</button>'+
            '</td>'+
        '</tr>';
    }).join('');

    area.innerHTML = '<div style="margin-bottom:10px;color:#64748b;font-size:calc(13.2px * var(--macs-font-scale, 1));">常用员工</div>'+
        '<div style="background:#fff;border:1px solid #e5e7eb;border-radius:8px;overflow:hidden;">'+
            '<table style="width:100%;border-collapse:collapse;font-size:calc(14.3px * var(--macs-font-scale, 1));">'+
                '<thead><tr style="background:#f8fafc;color:#64748b;text-align:left;border-bottom:1px solid #e5e7eb;">'+
                    '<th style="padding:8px 12px;font-weight:600;">员工</th>'+
                    '<th style="padding:8px 12px;font-weight:600;">状态</th>'+
                    '<th style="padding:8px 12px;font-weight:600;">流程</th>'+
                    '<th style="padding:8px 12px;font-weight:600;">任务</th>'+
                    '<th style="padding:8px 12px;font-weight:600;">最近任务</th>'+
                    '<th style="padding:8px 12px;font-weight:600;text-align:right;">操作</th>'+
                '</tr></thead>'+
                '<tbody>'+rows+'</tbody>'+
            '</table>'+
        '</div>';

    area.onclick = function(evt) {
        var btn = evt.target.closest('button[data-ae-action]');
        if (!btn) return;
        var id = btn.getAttribute('data-ae-id') || "";
        var employee = window._aeWorkbenchEmployeesById[id] || {};
        if (btn.getAttribute('data-ae-action') === 'detail') {
            if (typeof openAEDetailModal === 'function') {
                openAEDetailModal(id);
            } else if (typeof loadAEDetail === 'function') {
                loadAEDetail(id);
            }
            return;
        }
        var panel = document.getElementById('pipelinePanel');
        if (panel) panel.style.display = 'none';
        var input = document.getElementById('messageInput');
        if (input) {
            input.value = '@' + (employee.at_name || employee.name || id) + ' ' + aeDefaultTask(id);
            input.focus();
        }
    };
}

// ====================== DE CRUD Helpers ======================
// AE CRUD: delete only. Add/edit uses ae_panel.js modal (openAEEditModal).

async function deleteDE(id) {
    if (!confirm('确认删除AI员工: ' + id + '?')) return;
    try {
        var resp = await fetch('/api/ae/delete?id=' + encodeURIComponent(id), { method: 'POST' });
        var data = await resp.json();
        if (data.ok) {
            aeWorkLogCache = null;
            openAEPanel();
        } else {
            alert('删除失败: ' + (data.error || 'unknown'));
        }
    } catch(e) { alert('删除失败: ' + e.message); }
}

// ====================== 工作流编辑器 ======================

var pipeStepCounter = 0;

function addPipelineDBConnection(prefill) {
    prefill = prefill || {};
    var container = document.getElementById('pipeDatabaseConnections');
    if (!container) return;
    var row = document.createElement('div');
    row.className = 'pipe-db-connection';
    row.style.cssText = 'display:grid;grid-template-columns:1fr 90px 1.25fr 110px;gap:5px;padding:8px;margin-bottom:6px;background:#fff;border:1px solid #dbeafe;border-radius:6px;';
    var permissions = ['readonly'];
    var drivers = ['mysql','sqlite','postgres','sqlserver'];
    row.innerHTML =
        '<input data-db-field="id" value="'+escHtml(prefill.id||'')+'" placeholder="连接ID，如 customer_db" title="工作流内唯一连接ID" style="padding:5px;border:1px solid #d1d5db;border-radius:4px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
        '<select data-db-field="driver" style="padding:5px;border:1px solid #d1d5db;border-radius:4px;font-size:calc(11px * var(--macs-font-scale, 1));">'+drivers.map(function(v){return '<option value="'+v+'"'+(prefill.driver===v?' selected':'')+'>'+v+'</option>';}).join('')+'</select>'+
        '<input data-db-field="dsn_env" value="'+escHtml(prefill.dsn_env||'')+'" placeholder="环境变量，如 OPENKITTY_DB_DSN" title="数据库连接串所在的环境变量" style="padding:5px;border:1px solid #d1d5db;border-radius:4px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
        '<select data-db-field="permission" style="padding:5px;border:1px solid #d1d5db;border-radius:4px;font-size:calc(11px * var(--macs-font-scale, 1));">'+permissions.map(function(v){return '<option value="'+v+'"'+((prefill.permission||'readonly')===v?' selected':'')+'>'+v+'</option>';}).join('')+'</select>'+
        '<input data-db-field="allowed_tables" value="'+escHtml((prefill.allowed_tables||[]).join(','))+'" placeholder="允许的表/视图，逗号分隔" style="grid-column:1/3;padding:5px;border:1px solid #d1d5db;border-radius:4px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
        '<input data-db-field="denied_columns" value="'+escHtml((prefill.denied_columns||[]).join(','))+'" placeholder="脱敏字段，逗号分隔" style="grid-column:3/5;padding:5px;border:1px solid #d1d5db;border-radius:4px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
        '<label style="font-size:calc(11px * var(--macs-font-scale, 1));display:flex;align-items:center;gap:3px;"><input type="checkbox" data-db-field="enabled" '+(prefill.enabled?'checked':'')+'> 启用</label>'+
        '<label style="font-size:calc(11px * var(--macs-font-scale, 1));display:flex;align-items:center;gap:3px;">最大行数 <input type="number" data-db-field="max_rows" value="'+(prefill.max_rows||500)+'" min="1" max="50000" style="width:64px;padding:3px;border:1px solid #d1d5db;border-radius:3px;"></label>'+
        '<label style="font-size:calc(11px * var(--macs-font-scale, 1));display:flex;align-items:center;gap:3px;">超时秒数 <input type="number" data-db-field="timeout_seconds" value="'+(prefill.timeout_seconds||20)+'" min="1" max="300" style="width:58px;padding:3px;border:1px solid #d1d5db;border-radius:3px;"></label>'+
        '<button onclick="this.closest(\'.pipe-db-connection\').remove()" style="justify-self:end;padding:3px 8px;background:#fee2e2;color:#dc2626;border:1px solid #fecaca;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">删除</button>';
    container.appendChild(row);
}

function collectPipelineDBConnections() {
    var result = [];
    document.querySelectorAll('#pipeDatabaseConnections .pipe-db-connection').forEach(function(row) {
        var value = function(name) {
            var el = row.querySelector('[data-db-field="'+name+'"]');
            return el ? el.value.trim() : '';
        };
        var id = value('id');
        if (!id) return;
        var enabledEl = row.querySelector('[data-db-field="enabled"]');
        result.push({
            id: id,
            enabled: !!(enabledEl && enabledEl.checked),
            driver: value('driver'),
            dsn_env: value('dsn_env'),
            permission: value('permission') || 'readonly',
            allowed_tables: value('allowed_tables').split(',').map(function(s){return s.trim();}).filter(Boolean),
            denied_columns: value('denied_columns').split(',').map(function(s){return s.trim();}).filter(Boolean),
            max_rows: parseInt(value('max_rows'), 10) || 500,
            timeout_seconds: parseInt(value('timeout_seconds'), 10) || 20
        });
    });
    return result;
}

function syncToolPicks(el) {
    // el can be a stepNum (legacy) or a checkbox element
    var container;
    if (typeof el === 'number') {
        container = document.querySelector('#pipeSteps .pipe-step-card:nth-child('+el+')');
        if (!container) { el = document.querySelector('#pipeSteps input[type=checkbox][value]:checked'); if (el) container = el.closest('.pipe-step-card'); }
    } else {
        container = el.closest('.pipe-step-card');
    }
    if (!container) return;
    var checkboxes = container.querySelectorAll('input[type=checkbox][value]:checked');
    var picked = [];
    checkboxes.forEach(function(cb) { picked.push(cb.value); });
    var hidden = container.querySelector('input[data-field=tools]');
    if (hidden) hidden.value = picked.join(',');
    // Update label
    var label = container.querySelector('.tool-picker > div:first-child span:first-child');
    if (label) label.textContent = picked.length > 0 ? '▼ 选择工具 (已选:'+picked.length+'个)' : '▶ 选择工具 (已选:0个)';
    // Update tags
    var tagSpan = container.querySelector('[id^="toolPicked_"]');
    if (tagSpan) {
        tagSpan.innerHTML = picked.slice(0,6).map(function(t){return '<span style="background:#e0e7ff;padding:0 4px;border-radius:3px;">'+t+'</span>';}).join('') +
            (picked.length > 6 ? '<span style="color:#94a3b8;"> +'+(picked.length-6)+'</span>' : '');
    }
}

function addPipeStep(prefill) {
    pipeStepCounter++;
    prefill = prefill || {};
    var container = document.getElementById('pipeSteps');
    var div = document.createElement('div');
    div.className = 'pipe-step-card';
    div.style.cssText = 'background:#fff;border:1px solid #e5e7eb;border-radius:10px;padding:14px;margin-bottom:10px;';
    var stepNum = pipeStepCounter;

    var toolPickerHtml = '';
    var toolGroups = [
        {name:'📁 文件操作', tools:['read_file','write_file','edit_file','listdir']},
        {name:'🔍 搜索查找', tools:['grep','glob','web_search','web_fetch']},
        {name:'💻 系统命令', tools:['persistent_shell','cli','sys']},
        {name:'🧠 智能与协作', tools:['sub_agent','assemble_team','plan_task','update_plan_step']},
        {name:'📚 知识检索', tools:['knowledge_query','rag_search','search_history']},
        {name:'🗄️ 数据库', tools:['db_query']},
        {name:'📧 邮件与文档', tools:['email','extract','notepad']},
        {name:'🎤 媒体感知', tools:['speak','listen','watch','camera','screenshot','image_process']},
        {name:'⚙️ 其他', tools:['snapshot','code_check','ptc_execute','tool_detail','list_skills','external_agent','audio','remote']}
    ];
    var prefillToolsArr = (prefill.tools||'').split(',').filter(Boolean);
    toolGroups.forEach(function(g) {
        toolPickerHtml += '<div style="margin-bottom:6px;"><div style="font-size:calc(11px * var(--macs-font-scale, 1));font-weight:600;color:#94a3b8;margin-bottom:3px;">'+g.name+'</div><div style="display:flex;flex-wrap:wrap;gap:3px;">';
        g.tools.forEach(function(t) {
            var checked = prefillToolsArr.indexOf(t) >= 0 ? ' checked' : '';
            toolPickerHtml += '<label style="font-size:calc(11px * var(--macs-font-scale, 1));display:flex;align-items:center;gap:2px;padding:2px 6px;background:#f8fafc;border:1px solid #e5e7eb;border-radius:4px;cursor:pointer;white-space:nowrap;"><input type="checkbox" value="'+t+'"'+checked+' onchange="syncToolPicks(this)" style="margin:0;">'+t+'</label>';
        });
        toolPickerHtml += '</div></div>';
    });

    div.innerHTML =
        '<div style="display:flex;justify-content:space-between;align-items:center;cursor:pointer;margin-bottom:8px;" onclick="var b=this.nextElementSibling;b.style.display=b.style.display==\'none\'?\'\':\'none\'">' +
        '<b style="font-size:calc(15.4px * var(--macs-font-scale, 1));">#' + stepNum + ' ' + (prefill.name || '新步骤') + '</b>' +
        '<span style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#94a3b8;">▲ 展开编辑</span>' +
        '</div>' +
        '<div style="display:grid;grid-template-columns:1fr 1fr;gap:8px;">' +
        '<div><label style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">步骤名称</label><input data-field="name" value="' + (prefill.name||'') + '" placeholder="如：信息采集" style="width:100%;padding:6px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));"></div>' +
        '<div><label style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">Agent</label><input data-field="agent" value="' + (prefill.agent||'') + '" placeholder="如：openkitty1（可选）" style="width:100%;padding:6px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));"></div>' +
        '<div style="grid-column:1/-1;"><label style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">任务指令 (Prompt)</label><textarea data-field="prompt" rows="3" placeholder="这个步骤要做什么？写清楚给Agent的指令..." style="width:100%;padding:6px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));resize:vertical;">' + (prefill.prompt||'') + '</textarea></div>' +
        '<div style="grid-column:1/-1;">' +
	        '<div style="display:flex;align-items:center;gap:6px;margin-bottom:4px;">' +
	            '<label style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">可用工具</label>' +
	            '<span id="toolPicked_'+stepNum+'" style="font-size:calc(11px * var(--macs-font-scale, 1));color:#6366f1;display:flex;flex-wrap:wrap;gap:2px;"></span>' +
	        '</div>' +
	        '<div class="tool-picker" style="border:1px solid #e5e7eb;border-radius:8px;overflow:hidden;">' +
	            '<div onclick="var b=this.nextElementSibling;b.style.display=b.style.display==\'none\'?\'block\':\'none\';this.querySelector(\'span\').textContent=b.style.display==\'none\'?\'▶ 选择工具 (已选:\'+this.parentElement.querySelector(\'input[data-field=tools]\').value.split(\',\').filter(Boolean).length+\'个)\':\'▼ 收起\';" style="cursor:pointer;padding:8px 10px;background:#f8fafc;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#2563eb;display:flex;justify-content:space-between;">' +
	                '<span>▶ 选择工具 (已选:' + (prefill.tools ? prefill.tools.split(',').filter(Boolean).length : 0) + '个)</span>' +
	                '<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#94a3b8;">点击展开</span>' +
	            '</div>' +
	            '<div class="tool-picker-list" style="display:none;max-height:180px;overflow-y:auto;padding:8px;">' +
	toolPickerHtml +
	'</div></div><input type="hidden" data-field="tools" value="'+(prefill.tools||'')+'"><div style="font-size:calc(11px * var(--macs-font-scale, 1));color:#94a3b8;margin-top:3px;">勾选工具限制本步骤能用的能力，留空则允许全部</div></div> ' +
        '<div><label style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">最大迭代</label><input data-field="maxIter" type="number" value="' + (prefill.maxIter||'10') + '" min="1" max="100" style="width:80px;padding:6px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));"></div>' +
        '<div><label style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">数据库连接</label><input data-field="databaseConnection" value="' + escHtml(prefill.databaseConnection||'') + '" placeholder="连接ID（仅 db_query 步骤）" style="width:100%;padding:6px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));"></div>' +
        '<div style="grid-column:1/-1;display:flex;gap:16px;align-items:center;">' +
        '<label style="font-size:calc(12.1px * var(--macs-font-scale, 1));display:flex;align-items:center;gap:4px;"><input type="checkbox" data-field="requireApproval" ' + (prefill.requireApproval?'checked':'') + '> 需要审批</label>' +
        '<label style="font-size:calc(12.1px * var(--macs-font-scale, 1));display:flex;align-items:center;gap:4px;">模型 <input data-field="model" value="' + (prefill.model||'') + '" placeholder="默认" style="width:120px;padding:4px;border:1px solid #e5e7eb;border-radius:4px;font-size:calc(12.1px * var(--macs-font-scale, 1));"></label>' +
        '<button onclick="this.parentElement.parentElement.parentElement.remove()" style="margin-left:auto;background:#fee2e2;color:#dc3545;border:1px solid #fecaca;border-radius:6px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));padding:4px 10px;">删除此步骤</button>' +
        '</div>' +
        '</div>';
    container.appendChild(div);
}

// ====================== Route row helpers ======================

function renderRouteRow(stepNum, idx, r) {
	r = r || {};
	return '<div style="display:flex;gap:4px;align-items:center;margin-bottom:3px;" id="route_'+stepNum+'_'+idx+'">'+
		'<input value="'+(r.when||'')+'" placeholder="条件 (如: contains AGENT:pre_sales)" style="flex:1;padding:3px 6px;border:1px solid #e5e7eb;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
		'<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#6b7280;">→</span>'+
		'<input value="'+(r.goto||'')+'" placeholder="目标步骤名" style="flex:1;padding:3px 6px;border:1px solid #e5e7eb;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
		'<button onclick="this.parentElement.remove()" style="padding:1px 4px;background:#fef2f2;color:#ef4444;border:1px solid #fecaca;border-radius:2px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">×</button>'+
		'</div>';
}

function addRouteRow(stepNum) {
	var container = document.getElementById('stepRoutes_'+stepNum);
	var idx = Date.now();
	var div = document.createElement('div');
	div.innerHTML = renderRouteRow(stepNum, idx, {});
	container.appendChild(div.firstElementChild);
}

function renderStepExampleRow(stepNum, idx, e) {
	e = e || {};
	return '<div style="display:flex;gap:4px;align-items:center;margin-bottom:3px;" id="stepEx_'+stepNum+'_'+idx+'">'+
		'<input value="'+(e.user_message||'')+'" placeholder="用户消息 (如: 我的快递到哪了)" style="flex:2;padding:3px 6px;border:1px solid #e5e7eb;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
		'<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#6b7280;">→</span>'+
		'<input value="'+(e.route_to||'')+'" placeholder="路由到 (如: AGENT:after_sales)" style="flex:1;padding:3px 6px;border:1px solid #e5e7eb;border-radius:3px;font-size:calc(11px * var(--macs-font-scale, 1));">'+
		'<button onclick="this.parentElement.remove()" style="padding:1px 4px;background:#fef2f2;color:#ef4444;border:1px solid #fecaca;border-radius:2px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">×</button>'+
		'</div>';
}

function addStepExampleRow(stepNum) {
	var container = document.getElementById('stepExamples_'+stepNum);
	var idx = Date.now();
	var div = document.createElement('div');
	div.innerHTML = renderStepExampleRow(stepNum, idx, {});
	container.appendChild(div.firstElementChild);
}

function collectStepRoutes(stepNum) {
	var routes = [];
	var container = document.getElementById('stepRoutes_'+stepNum);
	if (!container) return routes;
	var rows = container.querySelectorAll('[id^="route_"]');
	rows.forEach(function(row) {
		var inputs = row.querySelectorAll('input');
		var when = inputs[0].value.trim();
		var goto = inputs[1].value.trim();
		if (when || goto) routes.push({when: when, goto: goto});
	});
	return routes;
}

function collectStepExamples(stepNum) {
	var examples = [];
	var container = document.getElementById('stepExamples_'+stepNum);
	if (!container) return examples;
	var rows = container.querySelectorAll('[id^="stepEx_"]');
	rows.forEach(function(row) {
		var inputs = row.querySelectorAll('input');
		var msg = inputs[0].value.trim();
		var routeTo = inputs[1].value.trim();
		if (msg && routeTo) examples.push({user_message: msg, route_to: routeTo});
	});
	return examples;
}

function loadPipelineToEditor(p) {
    document.getElementById('pipeName').value = p.name || '';
    document.getElementById('pipeDesc').value = p.desc || '';
    document.getElementById('pipeSteps').innerHTML = '';
    document.getElementById('pipeDatabaseConnections').innerHTML = '';
    (p.database_connections||[]).forEach(function(connection) { addPipelineDBConnection(connection); });
    if (p.steps) {
        p.steps.forEach(function(s, idx) { addPipeStep({
            name: s.name||"", agent: s.agent||"", prompt: s.prompt||"",
            tools: (s.tools||[]).join(","), maxIter: s.max_iter||10,
            requireApproval: s.require_approval||false, model: s.model||"",
            databaseConnection: s.database_connection||"",
            kb_slugs: (s.kb_slugs||[]).join(","),
            routes: s.routing||[], stepExamples: s.step_examples||[]
        }); });
    }
    // Populate routing + examples after steps are rendered
    setTimeout(function() {
        if (!p.steps) return;
        p.steps.forEach(function(s, idx) {
            var sids = document.querySelectorAll("#pipeSteps [id^=stepRoutes_]");
            if (idx >= sids.length) return;
            var snum = parseInt(sids[idx].id.split("_")[1]);
            if (s.routing) {
                s.routing.forEach(function(r) {
                    addRouteRow(snum);
                    var rows = document.querySelectorAll("#stepRoutes_" + snum + " [id^=route_]");
                    var last = rows[rows.length - 1];
                    var inputs = last.querySelectorAll("input");
                    if (inputs[0]) inputs[0].value = r.when || "";
                    if (inputs[1]) inputs[1].value = r.goto || "";
                });
            }
            if (s.step_examples) {
                s.step_examples.forEach(function(e) {
                    addStepExampleRow(snum);
                    var rows = document.querySelectorAll("#stepExamples_" + snum + " [id^=stepEx_]");
                    var last = rows[rows.length - 1];
                    var inputs = last.querySelectorAll("input");
                    if (inputs[0]) inputs[0].value = e.user_message || "";
                    if (inputs[1]) inputs[1].value = e.route_to || "";
                });
            }
        });
    }, 100);
}

async function savePipeline() {
    var name = document.getElementById('pipeName').value.trim();
    if (!name) { alert('请输入工作流名称'); return; }

    var steps = [];
    var stepDivs = document.getElementById('pipeSteps').children;
    for (var i = 0; i < stepDivs.length; i++) {
        var inputs = stepDivs[i].querySelectorAll('[data-field]');
        var step = {};
        for (var j = 0; j < inputs.length; j++) {
            var field = inputs[j].getAttribute('data-field');
            if (inputs[j].type === 'checkbox') step[field] = inputs[j].checked;
            else step[field] = inputs[j].value.trim();
        }
        if (step.name) {
            if (step.tools) step.tools = step.tools.split(',').map(function(s){return s.trim();}).filter(Boolean);
            else step.tools = [];
            if (step.maxIter) step.max_iter = parseInt(step.maxIter) || 10;
            delete step.maxIter;
            if (step.databaseConnection) step.database_connection = step.databaseConnection;
            delete step.databaseConnection;
            // Collect step-level routing + few-shot
            var sid = stepDivs[i].querySelector("[id^=stepRoutes_]");
            if (sid) {
                var snum = parseInt(sid.id.split("_")[1]);
                var routes = collectStepRoutes(snum);
                if (routes.length > 0) step.routing = routes;
                var examples = collectStepExamples(snum);
                if (examples.length > 0) step.step_examples = examples;
            }
            steps.push(step);
        }
    }
    
    var pipeline = {
        name: name,
        description: document.getElementById('pipeDesc').value.trim(),
        database_connections: collectPipelineDBConnections(),
        steps: steps
    };
    
    // 定时调度
    var schedule = document.getElementById('pipeSchedule').value.trim();
    if (schedule) {
        pipeline.schedule = schedule;
    }

    try {
        var resp = await fetch('/api/pipeline/save', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify(pipeline)
        });
        var data = await resp.json();
        if (data.ok) {
            var msg = '工作流已保存: ' + name;
            if (schedule) msg += '\n定时调度已设置: ' + schedule;
            alert(msg);
            loadExistingPipelines();
        } else {
            alert('保存失败: ' + (data.error || 'unknown'));
        }
    } catch(e) {
        alert('保存失败: ' + e.message);
    }
}

async function loadExistingPipelines() {
    try {
        var resp = await fetch('/api/pipeline/list');
        var data = await resp.json();
        var html = '';
        if (data.pipelines && data.pipelines.length > 0) {
            for (var i = 0; i < data.pipelines.length; i++) {
                var p = data.pipelines[i];
                var stepCount = p.steps ? p.steps.length : 0;
                html += '<div style="background:#f8f9fa;border-radius:8px;padding:10px;margin-bottom:6px;">' +
                    '<b>' + p.name + '</b> <span style="color:#6b7280;font-size:calc(12.1px * var(--macs-font-scale, 1));">' + stepCount + '步骤</span>' +
                    '<div style="margin-top:4px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">' + (p.desc||p.description||'') + '</div>' +
                    '<button onclick="event.stopPropagation();loadPipelineToEditor(' + JSON.stringify(p).replace(/"/g, '&quot;') + ')" style="margin-right:4px;background:#6366f1;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));padding:2px 8px;">编辑</button>' +
                    '<button onclick="deletePipeline(\'' + p.name + '\')" style="background:#dc3545;color:#fff;border:none;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));padding:2px 8px;">删除</button>' +
                    '</div>';
            }
        } else {
            html = '<p style="color:#94a3b8;">暂无工作流模板。创建第一个吧。</p>';
        }
        document.getElementById('existingPipes').innerHTML = html;
    } catch(e) {
        document.getElementById('existingPipes').innerHTML = '<p style="color:#dc3545;">加载失败</p>';
    }
}

async function deletePipeline(name) {
    if (!confirm('确认删除工作流: ' + name + '?')) return;
    try {
        await fetch('/api/pipeline/save', {
            method: 'DELETE',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({name: name})
        });
        loadExistingPipelines();
    } catch(e) { alert('删除失败'); }
}

// Hook into tab switching
var origSwitchTab = switchPipelineTab;
switchPipelineTab = function(tab) {
    origSwitchTab(tab);
    if (tab === 'editor') {
        document.getElementById('pipelineEditorView').style.display = 'block';
        loadExistingPipelines();
    } else {
        document.getElementById('pipelineEditorView').style.display = 'none';
    }
};


// ====================== DE View Tabs ======================
var aeCurrentView = 'list';
function switchAEView(view) {
    aeCurrentView = view;
    var lb = document.getElementById('aeTabListBtn'); 
    var eb = document.getElementById('aeTabExecsBtn');
    var wb = document.getElementById('aeWorkbenchArea');
    var ef = document.getElementById('aeExecFrame');
    if (view === 'list') {
        if (lb) { lb.style.background = '#fff'; lb.style.color = '#6366f1'; lb.style.fontWeight = '600'; lb.style.boxShadow = '0 1px 2px rgba(0,0,0,.05)'; }
        if (eb) { eb.style.background = 'transparent'; eb.style.color = '#6b7280'; eb.style.fontWeight = '400'; eb.style.boxShadow = 'none'; }
        if (wb) wb.style.display = '';
        if (ef) ef.style.display = 'none';
    } else {
        if (lb) { lb.style.background = 'transparent'; lb.style.color = '#6b7280'; lb.style.fontWeight = '400'; lb.style.boxShadow = 'none'; }
        if (eb) { eb.style.background = '#fff'; eb.style.color = '#6366f1'; eb.style.fontWeight = '600'; eb.style.boxShadow = '0 1px 2px rgba(0,0,0,.05)'; }
        if (wb) wb.style.display = 'none';
        if (!ef) {
            var dc = document.getElementById('detailContent');
            if (dc) {
                ef = document.createElement('div'); ef.id = 'aeExecFrame';
                ef.style.cssText = 'max-height:60vh;overflow-y:auto;';
                dc.appendChild(ef);
            }
        }
        if (ef) { ef.style.display = ''; loadAEExecList(ef); }
    }
}
async function loadAEExecList(el) {
    el.innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">加载中...</div>';
    try {
        var resp = await fetch('/api/pipeline/executions');
        var data = await resp.json();
        var execs = sortExecutionsDesc(data.executions || []);
        if (execs.length === 0) { el.innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">暂无执行记录</div>'; return; }
        var icons = {running:'🔄',waiting_approval:'⏳',completed:'✅',failed:'❌',stopped:'⏹'};
        var colors = {running:'#6366f1',waiting_approval:'#f59e0b',completed:'#10b981',failed:'#ef4444',stopped:'#6b7280'};
        var html = '<table style="width:100%;border-collapse:collapse;font-size:calc(13.2px * var(--macs-font-scale, 1));"><thead><tr style="text-align:left;border-bottom:2px solid #e5e7eb;"><th style="padding:6px 8px;">工作流</th><th style="padding:6px 8px;">AI员工</th><th style="padding:6px 8px;">状态</th><th style="padding:6px 8px;">步骤</th><th style="padding:6px 8px;">时间</th><th style="padding:6px 8px;width:60px;"></th></tr></thead><tbody>';
        execs.forEach(function(e) {
            var st = e.status || '?'; var steps = e.steps || [];
            var done = steps.filter(function(s){return s.status==='completed';}).length;
            html += '<tr style="border-bottom:1px solid #f0f0f0;cursor:pointer;" onclick="showExecutionDetail(\''+escJs(e.id)+'\')"><td style="padding:6px 8px;font-weight:500;">'+(icons[st]||'')+' '+escHtml(e.pipeline_name||e.id)+'</td><td style="padding:6px 8px;color:#334155;">'+execEmployeeLabel(e)+'</td><td style="padding:6px 8px;color:'+(colors[st]||'#6b7280')+'">'+st+'</td><td style="padding:6px 8px;color:#6b7280;">'+done+'/'+steps.length+'</td><td style="padding:6px 8px;color:#9ca3af;font-size:calc(12.1px * var(--macs-font-scale, 1));">'+formatTime(e.created_at)+'</td><td style="padding:6px 8px;text-align:right;"><span style="color:#6366f1;font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:500;">查看 &rarr;</span></td></tr>';
        });
        html += '</tbody></table>'; el.innerHTML = html;
    } catch(e) { el.innerHTML = '<div style="color:#ef4444;padding:12px;">加载失败: '+escHtml(e.message)+'</div>'; }
}
