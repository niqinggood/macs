// ====================== RAG 检索系统 ======================

var ragModalHTML = `
<div id="knowledgeModal" class="modal">
    <div class="modal-content" style="max-width:900px;width:95vw;padding:0;overflow:hidden;">
        <div class="modal-header" style="padding:12px 24px;flex-wrap:wrap;gap:8px;">
            <h3 style="display:flex;align-items:center;gap:10px;font-size:calc(18.7px * var(--macs-font-scale, 1));margin:0;">
                <svg viewBox="0 0 24 24" width="22" height="22" stroke="#0284c7" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="flex-shrink:0;"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
                RAG 智能检索
                <span style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#9ca3af;background:#f3f4f6;padding:2px 8px;border-radius:4px;font-weight:400;">检索测试</span>
                <span onclick="switchRagAdmin('kbs')" style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#0284c7;cursor:pointer;font-weight:400;" title="跳转到管理页面"><svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:middle;margin-right:2px;"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>管理</span>
            </h3>
            <button type="button" class="knowledge-accumulated-button" data-knowledge-accumulated aria-haspopup="dialog" aria-controls="memoryPanelModal" onclick="closeKnowledgeModal();openMemoryPanel('knowledge');" style="font:inherit;font-size:calc(14.3px * var(--macs-font-scale, 1));padding:6px 10px;border:1px solid #cbd5e1;border-radius:8px;background:transparent;color:inherit;cursor:pointer;white-space:nowrap;">AI积累</button>
            <span class="close-modal" onclick="closeKnowledgeModal()">&times;</span>
        </div>

        <!-- 板块选择 + 操作 -->
        <div style="padding:14px 24px;background:#fafbfc;border-bottom:1px solid #f0f0f0;display:flex;align-items:center;gap:10px;flex-wrap:wrap;">
            <select id="ragKbSelect" onchange="ragKbChanged()" style="padding:8px 14px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#374151;background:#fff;min-width:200px;">
                <option value="">全部板块</option>
            </select>
            <span id="ragKbInfo" style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#9ca3af;"></span>
            <span id="ragBleveStatus" style="font-size:calc(12.1px * var(--macs-font-scale, 1));cursor:pointer;margin-left:8px;" onclick="ragCheckHealth()" title="点击刷新 Bleve 索引状态">🔍</span>
            <div style="flex:1;"></div>
            <button onclick="ragClear()" style="padding:7px 12px;background:#fff;border:1px solid #e5e7eb;border-radius:8px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;">清屏</button>
            <button onclick="loadRagKbList()" style="padding:7px 12px;background:#fff;border:1px solid #e5e7eb;border-radius:8px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;" title="刷新">刷新</button>
        </div>

        <!-- 对话区 -->
        <div id="ragMessages" style="flex:1;overflow-y:auto;padding:20px 24px;min-height:350px;max-height:45vh;background:#fff;font-size:calc(14.3px * var(--macs-font-scale, 1));">
            <div class="rag-msg assistant">
                <div class="rag-avatar">AI</div>
                <div class="rag-bubble">
                    检索测试 — 选板块输问题，验证索引效果<br><br>
                    <b>怎么搜？</b><br>
                    • 输入自然语言问题，自动分词后检索<br>
                    • 多个关键词用空格分隔<br>
                    • 例：<code>大模型 部署</code> 或 <code>Transformer 详解</code><br><br>
                    <b>板块操作：</b><br>
                    • 顶部按钮管理板块、上传文档<br>
                    • AI员工挂载板块后自动调用检索 API
                </div>
            </div>
        </div>

        <!-- 输入区 -->
        <div style="padding:12px 20px;border-top:1px solid #f0f0f0;display:flex;gap:10px;align-items:flex-end;">
            <textarea id="ragInput" rows="1" placeholder="输入检索问题..."
                style="flex:1;padding:10px 14px;border:1px solid #e5e7eb;border-radius:12px;font-size:calc(14.3px * var(--macs-font-scale, 1));resize:none;line-height:1.5;font-family:inherit;"
                onkeydown="if(event.key==='Enter' && !event.shiftKey){event.preventDefault();ragSend();}"
                oninput="this.style.height='auto';this.style.height=Math.min(this.scrollHeight,120)+'px';"></textarea>
            <button onclick="ragSend()" id="ragSendBtn"
                style="width:42px;height:42px;background:linear-gradient(135deg,#0284c7,#0284c7);color:#fff;border:none;border-radius:10px;cursor:pointer;font-size:calc(17.6px * var(--macs-font-scale, 1));flex-shrink:0;"
                title="发送 (Enter)">➤</button>
        </div>

    </div>
</div>

<!-- RAG 管理面板 -->
<div id="ragAdminPanel" class="modal" style="z-index:2100;">
    <div class="modal-content" style="max-width:1100px;width:96vw;padding:0;overflow:hidden;">
        <div class="modal-header" style="padding:14px 24px;flex-wrap:wrap;gap:8px;">
            <h3 style="font-size:calc(18.7px * var(--macs-font-scale, 1));margin:0;display:flex;align-items:center;gap:10px;">
                <svg viewBox="0 0 24 24" width="22" height="22" stroke="#0284c7" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="flex-shrink:0;"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>
                RAG 板块知识库管理
                <span id="ragBleveStatus2" style="font-size:calc(11px * var(--macs-font-scale, 1));cursor:pointer;margin-left:4px;" onclick="ragCheckHealth()" title="Bleve索引状态">🔍</span>
                <span onclick="closeRagAdmin();openKnowledgePanel();" style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#0284c7;cursor:pointer;font-weight:400;" title="跳转到检索页面"><svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="vertical-align:middle;margin-right:2px;"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>检索</span>
            </h3>
            <button type="button" class="knowledge-accumulated-button" data-knowledge-accumulated aria-haspopup="dialog" aria-controls="memoryPanelModal" onclick="closeRagAdmin();openMemoryPanel('knowledge');" style="font:inherit;font-size:calc(14.3px * var(--macs-font-scale, 1));padding:6px 10px;border:1px solid #cbd5e1;border-radius:8px;background:transparent;color:inherit;cursor:pointer;white-space:nowrap;">AI积累</button>
            <span class="close-modal" onclick="closeRagAdmin()">&times;</span>
        </div>
        <!-- 标签栏 -->
        <div style="display:flex;gap:0;padding:0 24px;background:#fff;border-bottom:1px solid #e5e7eb;">
            <span class="rag-admin-tab active" onclick="switchRagAdminTab('kbs',this)" style="padding:10px 20px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#0284c7;border-bottom:2px solid #0284c7;font-weight:500;">板块管理</span>
            <span class="rag-admin-tab" onclick="switchRagAdminTab('docs',this)" style="padding:10px 20px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#6b7280;border-bottom:2px solid transparent;">文档管理</span>
            <span class="rag-admin-tab" onclick="switchRagAdminTab('faq',this)" style="padding:10px 20px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#6b7280;border-bottom:2px solid transparent;">FAQ</span>
            <span class="rag-admin-tab" onclick="switchRagAdminTab('chunks',this)" style="padding:10px 20px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));color:#6b7280;border-bottom:2px solid transparent;">分块策略</span>
        </div>
        <div style="padding:24px;overflow-y:auto;max-height:70vh;font-size:calc(14.3px * var(--macs-font-scale, 1));">

            <!-- 板块管理 -->
            <div id="ragAdminKbs">
                <div style="display:flex;gap:8px;margin-bottom:12px;align-items:center;flex-wrap:wrap;">
                    <input id="ragNewKbName" placeholder="新建板块名称" style="flex:1;padding:8px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:140px;">
                    <input id="ragNewKbDesc" placeholder="描述（可选）" style="flex:1;padding:8px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:140px;">
                    <button onclick="ragCreateKb()" style="padding:8px 16px;background:#0284c7;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));white-space:nowrap;">创建板块</button>
                    <span style="display:flex;gap:2px;background:#f3f4f6;border-radius:6px;padding:2px;">
                        <span onclick="ragAdminView='list';loadRagAdminKbs()" id="ragViewList" style="padding:4px 8px;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#374151;background:#fff;">☰ 列表</span>
                        <span onclick="ragAdminView='card';loadRagAdminKbs()" id="ragViewCard" style="padding:4px 8px;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;">▦ 卡片</span>
                    </span>
                </div>
                <!-- Search Test Panel -->
<div id="ragSearchTest" style="display:none;margin-bottom:16px;padding:16px;background:#f9fafb;border-radius:8px;">
    <h4 style="margin:0 0 8px;font-size:calc(14.3px * var(--macs-font-scale, 1));">搜索测试</h4>
    <div style="display:flex;gap:8px;margin-bottom:8px;">
        <input id="ragTestQuery" placeholder="输入搜索问题..." style="flex:1;padding:8px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));" onkeydown="if(event.key==='Enter')ragTestSearch()">
        <select id="ragTestKb" style="padding:8px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:120px;"><option value="">全部 KB</option></select>
        <button onclick="ragTestSearch()" style="padding:8px 16px;background:#0284c7;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">搜索</button>
    </div>
    <div id="ragTestResults" style="font-size:calc(13.2px * var(--macs-font-scale, 1));max-height:300px;overflow-y:auto;"></div>
</div>
<div id="ragAdminKbList"><div style="text-align:center;padding:24px;color:#9ca3af;">加载中...</div></div>
            </div>

            <!-- 文档管理 -->
            <div id="ragAdminDocs" style="display:none;">
                <div style="margin-bottom:10px;display:flex;gap:8px;align-items:center;flex-wrap:wrap;">
                    <select id="ragDocKbSelect" onchange="loadRagDocs()" style="padding:7px 12px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:180px;">
                        <option value="">选择板块</option>
                    </select>
                    <label style="cursor:pointer;padding:7px 14px;background:#fff;color:#0284c7;border:1px solid #0284c7;border-radius:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));display:inline-flex;align-items:center;gap:4px;font-weight:500;">
                        <svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" y1="3" x2="12" y2="15"/></svg>上传文档<input type="file" id="ragAdminFileInput" onchange="ragUploadDoc()" style="display:none;" accept=".txt,.md,.pdf,.docx,.csv,.xlsx,.html,.htm">
                    </label>
                    <span style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#94a3b8;">支持 txt md pdf docx xlsx html csv</span>
                </div>
                <div id="ragAdminDocsList"><div style="text-align:center;padding:24px;color:#9ca3af;">请先选择板块</div></div>
                <div id="ragChunkPanel" style="display:none;margin-top:12px;padding:14px;background:#fff;border:1px solid #e5e7eb;border-radius:10px;"></div>
            </div>

            <!-- FAQ管理 -->
            <div id="ragAdminFaq" style="display:none;">
                <div style="display:flex;gap:8px;margin-bottom:12px;align-items:center;flex-wrap:wrap;">
                    <select id="ragFaqKbSelect" onchange="loadRagFaqs()" style="padding:7px 12px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:180px;">
                        <option value="">选择板块</option>
                    </select>
                    <input id="ragFaqQuestion" placeholder="问题" style="flex:1;padding:7px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:140px;">
                    <input id="ragFaqAnswer" placeholder="答案" style="flex:1;padding:7px;border:1px solid #e5e7eb;border-radius:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:140px;">
                    <button onclick="ragCreateFaq()" style="padding:7px 14px;background:#0284c7;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));">添加FAQ</button>
                </div>
                <div id="ragAdminFaqList"><div style="text-align:center;padding:24px;color:#9ca3af;">请先选择板块</div></div>
            </div>

            <!-- 分块策略 -->
            <div id="ragAdminChunks" style="display:none;">
                <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;">
                    <div style="background:#f8fafc;border:1px solid #e5e7eb;border-radius:8px;padding:14px;">
                        <div style="font-weight:600;font-size:calc(13.2px * var(--macs-font-scale, 1));margin-bottom:6px;">当前策略</div>
                        <div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#6b7280;line-height:1.8;">
                            切分方式: 按标题 + 段落边界<br>
                            最小块: <b>800</b> 字符<br>
                            最大块: <b>2000</b> 字符
                        </div>
                    </div>
                    <div style="background:#f8fafc;border:1px solid #e5e7eb;border-radius:8px;padding:14px;">
                        <div style="font-weight:600;font-size:calc(13.2px * var(--macs-font-scale, 1));margin-bottom:6px;">说明</div>
                        <div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;line-height:1.6;">
                            文档上传后按 Markdown 标题分段，段落过长时在段落边界拆分。上传同名文件自动覆盖旧版本。
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>
</div>

<style>
.rag-msg { display:flex;gap:10px;margin-bottom:16px; }
.rag-msg.user { flex-direction:row-reverse; }
.rag-msg .rag-avatar { width:32px;height:32px;border-radius:50%;display:flex;align-items:center;justify-content:center;color:#fff;font-size:calc(14.3px * var(--macs-font-scale, 1));flex-shrink:0; }
.rag-msg.assistant .rag-avatar { background:linear-gradient(135deg,#0284c7,#0284c7); }
.rag-msg.user .rag-avatar { background:#1677ff; }
.rag-msg .rag-bubble { max-width:82%;padding:10px 14px;border-radius:12px;font-size:calc(15.4px * var(--macs-font-scale, 1));line-height:1.7; }
.rag-msg.assistant .rag-bubble { background:#f5f3ff;color:#333; }
.rag-msg.user .rag-bubble { background:#1677ff;color:#fff; }
.rag-source { margin-top:6px;font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af; }
.rag-score-tag { display:inline-block;padding:2px 8px;border-radius:4px;font-size:calc(11px * var(--macs-font-scale, 1));font-weight:600;margin-left:6px; }
.rag-score-tag.high { background:#d1fae5;color:#065f46; }
.rag-score-tag.med  { background:#fef3c7;color:#92400e; }
.rag-score-tag.low  { background:#f3f4f6;color:#9ca3af; }
</style>`;

(function initRag() {
    if (!document.getElementById('knowledgeModal')) {
        document.body.insertAdjacentHTML('beforeend', ragModalHTML);
    }
})();

function translateKnowledgeAccumulationButtons() {
    var english = String(window.openKittyLanguage || document.documentElement.lang || 'zh').toLowerCase().startsWith('en');
    document.querySelectorAll('[data-knowledge-accumulated]').forEach(function(button) { button.textContent = english ? 'AI learning' : 'AI积累'; });
}
document.addEventListener('openkitty:languagechange', translateKnowledgeAccumulationButtons);
translateKnowledgeAccumulationButtons();

// ==================== Auth Token ====================

function ragAuthToken() {
    return sessionStorage.getItem('auth_token') || '';
}

function ragFetch(url, opts) {
    opts = opts || {};
    opts.headers = opts.headers || {};
    var token = ragAuthToken();
    if (token) opts.headers['X-Auth-Token'] = token;
    return fetch(url, opts);
}

// ==================== Main Modal ====================

function openKnowledgePanel() {
    document.getElementById('knowledgeModal').classList.add('active');
    loadRagKbList().then(function(){ ragCheckHealth(); });
    var ta = document.getElementById('ragInput');
    if (ta) setTimeout(function(){ ta.focus(); }, 200);
}

function openRagAdmin() {
    document.getElementById('ragAdminPanel').classList.add('active');
    switchRagAdminTab('kbs');
}


async function ragTestSearch() {
    var q = document.getElementById("ragTestQuery").value.trim();
    var kbId = document.getElementById("ragTestKb").value;
    if (!q) return;
    var body = {query: q, top_k: 5};
    if (kbId) body.kb_ids = [parseInt(kbId)];
    var resp = await ragFetch("/api/rag/chat", {method:"POST",body:JSON.stringify(body),headers:{"Content-Type":"application/json"}});
    var d = await resp.json();
    var html = "<table style=\"width:100%;border-collapse:collapse;\"><thead><tr style=\"text-align:left;border-bottom:1px solid #e5e7eb;\"><th style=\"padding:4px;\">#</th><th style=\"padding:4px;\">??</th><th style=\"padding:4px;\">??</th><th style=\"padding:4px;\">??</th></tr></thead><tbody>";
    (d.results||[]).forEach(function(r,i){
        var scorePct = Math.round(r.score/10*100);
        var color = scorePct > 70 ? "#10b981" : scorePct > 40 ? "#f59e0b" : "#9ca3af";
        html += "<tr style=\"border-bottom:1px solid #f8f8f8;\"><td style=\"padding:4px;\">"+(i+1)+"</td><td style=\"padding:4px;color:#6b7280;max-width:120px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;\">"+esc(r.source_doc)+"</td><td style=\"padding:4px;\"><span style=\"color:"+color+";font-weight:500;\">"+scorePct+"%</span></td><td style=\"padding:4px;max-width:400px;overflow:hidden;text-overflow:ellipsis;\">"+esc((r.content||"").substring(0,100))+"</td></tr>";
    });
    html += "</tbody></table>";
    document.getElementById("ragTestResults").innerHTML = html || "<div style=\"color:#9ca3af;text-align:center;padding:12px;\">???</div>";
}

async function ragUpdateWeight(kbId, weight) {
    await ragFetch("/api/rag/kb/" + kbId + "/weight", {method:"PUT",body:JSON.stringify({bm25_weight: weight}),headers:{"Content-Type":"application/json"}});
    loadRagAdminKbs();
}
function closeKnowledgeModal() {
    document.getElementById('knowledgeModal').classList.remove('active');
}

function closeRagAdmin() {
    document.getElementById('ragAdminPanel').classList.remove('active');
}

function switchRagAdmin(tab) {
    document.getElementById('ragAdminPanel').classList.add('active');
    ragCheckHealth();
    switchRagAdminTab(tab || 'kbs');
}

function switchRagAdminTab(tab, el) {
    document.querySelectorAll('.rag-admin-tab').forEach(function(t){
        t.classList.remove('active'); t.style.color='#6b7280'; t.style.borderBottomColor='transparent';
    });
    if (el) { el.classList.add('active'); el.style.color='#0284c7'; el.style.borderBottomColor='#0284c7'; }
    document.getElementById('ragAdminKbs').style.display = tab === 'kbs' ? '' : 'none';
    document.getElementById('ragAdminDocs').style.display = tab === 'docs' ? '' : 'none';
    document.getElementById('ragAdminFaq').style.display = tab === 'faq' ? '' : 'none';
    document.getElementById('ragAdminChunks').style.display = tab === 'chunks' ? '' : 'none';
    if (tab === 'kbs') loadRagAdminKbs();
    if (tab === 'docs') { loadRagDocKbSelect(); loadRagDocs(); }
    if (tab === 'faq') { loadRagFaqKbSelect(); loadRagFaqs(); }
}

// ==================== KB CRUD ====================

async function loadRagKbList() {
    try {
        var res = await ragFetch('/api/rag/kb/list');
        var data = await res.json();
        var kbs = data.kbs || [];
        var sel = document.getElementById('ragKbSelect');
        sel.innerHTML = '<option value="">全部板块 (' + kbs.length + ')</option>';
        for (var i = 0; i < kbs.length; i++) {
            var kb = kbs[i];
            sel.innerHTML += '<option value="' + kb.id + '">' + escHtml(kb.name) + ' (' + (kb.doc_count||0) + '篇)</option>';
        }
        document.getElementById('ragKbInfo').textContent = '共 ' + kbs.length + ' 个板块';
    } catch(e) { console.error('loadRagKbList', e); }
}

var ragSelectedKbId = '';

async function ragEnterKB(kbId, kbName) {
    switchRagAdminTab('docs'); // show tab first so DOM is visible
    await loadRagDocKbSelect();
    document.getElementById('ragDocKbSelect').value = kbId;
    loadRagDocs();
}

var ragHealthTimer = null;
async function ragCheckHealth() {
    var kbId = document.getElementById("ragKbSelect") ? (document.getElementById("ragKbSelect").value || "0") : "0";
    try {
        var resp = await ragFetch("/api/rag/health?kb_id=" + kbId);
        var d = await resp.json();
        document.getElementById("ragBleveStatus").innerHTML = d.consistent ? "\u2705" : "\u26a0\ufe0f";
        document.getElementById("ragBleveStatus").title = "Bleve:" + d.bleve_count + " DB:" + d.db_count + " Engine:" + d.engine;
        var hc = document.getElementById("ragHealthCard");
        if (hc) {
            hc.style.display = "block";
            document.getElementById("ragHealthBleve").textContent = "Bleve: " + (d.bleve_count || 0);
            document.getElementById("ragHealthDB").textContent = "DB: " + (d.db_count || 0);
            document.getElementById("ragHealthVector").textContent = "??: " + ((d.kb_stats && d.kb_stats.avg_chunk_len) ? "?" : "-") + (d.kb_stats && d.kb_stats.bm25_weight ? " | BM25??:" + Math.round(d.kb_stats.bm25_weight*100) + "%" : "");
            var zc = d.kb_stats ? d.kb_stats.zero_queries : 0;
            document.getElementById("ragHealthZero").textContent = zc > 0 ? "???: " + zc : "";
            document.getElementById("ragHealthStatus").textContent = d.consistent ? "??" : "???";
            document.getElementById("ragHealthStatus").style.color = d.consistent ? "#10b981" : "#ef4444";
        }
    } catch(e) {}

    var els = ['ragBleveStatus', 'ragBleveStatus2'];
    try {
        var res = await ragFetch('/api/rag/health');
        var d = await res.json();
        for (var j = 0; j < els.length; j++) {
            var el = document.getElementById(els[j]); if (!el) continue;
            if (d.ok) {
                if (d.consistent) {
                    el.textContent = '✅ DB ' + (d.db_count||0) + ' = Bleve ' + (d.bleve_count||0);
                    el.style.color = '#22c55e';
                    if (ragHealthTimer) { clearInterval(ragHealthTimer); ragHealthTimer = null; }
                } else {
                    el.textContent = '⏳ DB ' + (d.db_count||0) + ' → Bleve ' + (d.bleve_count||0);
                    el.style.color = '#f59e0b';
                    // Auto-poll every 2s until consistent
                    if (!ragHealthTimer) ragHealthTimer = setInterval(ragCheckHealth, 2000);
                }
                el.title = 'DB分块: ' + (d.db_count||0) + ' | Bleve索引: ' + (d.bleve_count||0) + ' | ' + (d.consistent ? '一致' : '请点板块重建');
            } else {
                el.textContent = '❌';
                el.style.color = '#dc3545';
                el.title = 'Bleve异常: ' + (d.error||'');
            }
        }
    } catch(e) {
        for (var j = 0; j < els.length; j++) {
            var el = document.getElementById(els[j]); if (!el) continue;
            el.textContent = '⚠️'; el.style.color = '#f59e0b';
        }
    }
}

function ragKbChanged() {
    ragSelectedKbId = document.getElementById('ragKbSelect').value;
}

var ragAdminView = 'list';

async function loadRagAdminKbs() {
    try {
        var res = await ragFetch('/api/rag/kb/list');
        var data = await res.json();
        var kbs = data.kbs || [];

        document.getElementById('ragViewList').style.background = ragAdminView === 'list' ? '#fff' : '';
        document.getElementById('ragViewList').style.color = ragAdminView === 'list' ? '#374151' : '#6b7280';
        document.getElementById('ragViewCard').style.background = ragAdminView === 'card' ? '#fff' : '';
        document.getElementById('ragViewCard').style.color = ragAdminView === 'card' ? '#374151' : '#6b7280';

        var html = '';
        if (ragAdminView === 'card') {
            html += '<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:10px;">';
            for (var i = 0; i < kbs.length; i++) {
                var kb = kbs[i];
                html += '<div style="background:#fff;border:1px solid #e5e7eb;border-radius:10px;padding:16px;cursor:pointer;" onclick="ragEnterKB(' + kb.id + ', \'' + escHtml(kb.name) + '\')">' +
                    '<div style="display:flex;justify-content:space-between;align-items:flex-start;margin-bottom:8px;">' +
                    '<b style="font-size:calc(15.4px * var(--macs-font-scale, 1));color:#1e293b;">' + escHtml(kb.name) + '</b>' +
                    '<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;background:#f3f4f6;padding:2px 6px;border-radius:4px;">' + (kb.build_status||'idle') + '</span>' +
                    '</div>' +
                    (kb.description ? '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;margin-bottom:8px;">' + escHtml(kb.description) + '</div>' : '') +
                    '<div style="display:flex;gap:16px;margin-bottom:10px;">' +
                    '<div><span style="font-weight:600;color:#0284c7;">' + (kb.doc_count||0) + '</span><span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;"> 文档</span></div>' +
                    '<div><span style="font-weight:600;color:#0284c7;">' + (kb.chunk_count||0) + '</span><span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;"> chunks</span></div>' +
                    '</div>' +
                    '<div style="display:flex;gap:4px;">' +
                    '<button onclick="event.stopPropagation();ragSyncIndex(' + kb.id + ')" title="DB chunk → Bleve" style="flex:1;padding:4px 8px;background:#e0e7ff;color:#4338ca;border:1px solid #c7d2fe;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">同步</button>' +
                    '<button onclick="event.stopPropagation();ragRebuildKb(' + kb.id + ')" title="重新分块+索引" style="flex:1;padding:4px 8px;background:#fef3c7;color:#92400e;border:1px solid #fcd34d;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">重建</button>' +
                    '<button onclick="event.stopPropagation();ragDeleteKb(' + kb.id + ',\'' + escHtml(kb.name) + '\')" style="padding:4px 8px;background:#fee2e2;color:#dc2626;border:1px solid #fecaca;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">删除</button>' +
                    '</div></div>';
            }
            html += '</div>';
        } else {
            for (var i = 0; i < kbs.length; i++) {
                var kb = kbs[i];
                html += '<div style="display:flex;justify-content:space-between;align-items:center;padding:10px 14px;background:#fff;border:1px solid #e5e7eb;border-radius:8px;margin-bottom:4px;">' +
                    '<div style="cursor:pointer;flex:1;" onclick="ragEnterKB(' + kb.id + ', \'' + escHtml(kb.name) + '\')">' +
                    '<b style="font-size:calc(14.3px * var(--macs-font-scale, 1));">' + escHtml(kb.name) + '</b>' +
                    '<span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;margin-left:8px;">' + (kb.doc_count||0) + ' 文档 | ' + (kb.chunk_count||0) + ' chunks</span>' +
                    (kb.description ? '<div style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;margin-top:2px;">' + escHtml(kb.description) + '</div>' : '') +
                    '</div>' +
                    '<div style="display:flex;gap:4px;flex-shrink:0;">' +
                    '<button onclick="event.stopPropagation();ragSyncIndex(' + kb.id + ')" title="同步Bleve" style="padding:4px 8px;background:#e0e7ff;color:#4338ca;border:1px solid #c7d2fe;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">同步</button>' +
                    '<button onclick="event.stopPropagation();ragRebuildKb(' + kb.id + ')" title="重新分块+索引" style="padding:4px 8px;background:#fef3c7;color:#92400e;border:1px solid #fcd34d;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">重建</button>' +
                    '<button onclick="event.stopPropagation();ragDeleteKb(' + kb.id + ',\'' + escHtml(kb.name) + '\')" style="padding:4px 8px;background:#fee2e2;color:#dc2626;border:1px solid #fecaca;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));">删除</button>' +
                    '</div></div>';
            }
        }
        document.getElementById('ragAdminKbList').innerHTML = html || '<div style="text-align:center;padding:24px;color:#9ca3af;">暂无板块</div>';
    } catch(e) {}
}

async function ragCreateKb() {
    var name = document.getElementById('ragNewKbName').value.trim();
    if (!name) { alert('请输入板块名称'); return; }
    var desc = document.getElementById('ragNewKbDesc').value.trim();
    try {
        var res = await ragFetch('/api/rag/kb/create', {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({name:name,description:desc})
        });
        var data = await res.json();
        if (data.ok) {
            document.getElementById('ragNewKbName').value = '';
            document.getElementById('ragNewKbDesc').value = '';
            loadRagAdminKbs();
            loadRagKbList();
        } else { alert('创建失败: ' + (data.error || '')); }
    } catch(e) { alert('创建失败: ' + e.message); }
}

async function ragDeleteKb(id, kbName) {
    var answer = prompt('⚠️ 删除「' + kbName + '」将同时删除所有文档、分块和文件，不可恢复。\n请输入板块名称确认删除：');
    if (answer !== kbName) { if (answer !== null) alert('名称不匹配，已取消'); return; }
    try {
        var res = await ragFetch('/api/rag/kb/' + id + '?confirm=yes', { method: 'DELETE' });
        var data = await res.json();
        if (data.ok) { loadRagAdminKbs(); loadRagKbList(); }
        else { alert('删除失败: ' + (data.error || '')); }
    } catch(e) { alert('删除失败: ' + e.message); }
}

async function ragSyncIndex(kbId) {
    if (!confirm('将DB中所有chunk批量同步到Bleve索引（不重新分块），确定？')) return;
    try {
        var res = await ragFetch('/api/rag/kb/' + kbId + '/sync-index', { method: 'POST' });
        var data = await res.json();
        if (data.ok) { alert('同步完成: ' + (data.indexed||0) + ' 个chunk已索引'); ragCheckHealth(); }
        else { alert('失败: ' + (data.error||'')); }
    } catch(e) { alert('失败: ' + e.message); }
}

async function ragRebuildKb(id) {
    if (!confirm('确认重建索引？将重新处理所有文档。')) return;
    try {
        var res = await ragFetch('/api/rag/kb/' + id + '/rebuild', { method: 'POST' });
        var data = await res.json();
        if (data.ok) {
            alert('重建完成: ' + (data.doc_count||0) + ' 文档, ' + (data.chunk_count||0) + ' chunks');
            loadRagAdminKbs();
            loadRagKbList();
        } else { alert('重建失败: ' + (data.error || '')); }
    } catch(e) { alert('重建失败: ' + e.message); }
}

// ==================== Doc Management ====================

async function loadRagDocKbSelect() {
    try {
        var res = await ragFetch('/api/rag/kb/list');
        var data = await res.json();
        var kbs = data.kbs || [];
        var sel = document.getElementById('ragDocKbSelect');
        sel.innerHTML = '<option value="">选择板块</option>';
        for (var i = 0; i < kbs.length; i++) {
            sel.innerHTML += '<option value="' + kbs[i].id + '">' + escHtml(kbs[i].name) + '</option>';
        }
    } catch(e) {}
}

var ragDocsCache = {all:[], kbId:0, page:1, pageSize:20, filter:''};

async function loadRagDocs() {
    var kbId = document.getElementById('ragDocKbSelect').value;
    if (!kbId) {
        document.getElementById('ragAdminDocsList').innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">请先选择板块</div>';
        return;
    }
    var div = document.getElementById('ragAdminDocsList');
    div.innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">加载中...</div>';
    try {
        var fres = await ragFetch('/api/rag/kb/' + kbId + '/files');
        var fdata = await fres.json();
        ragDocsCache.all = fdata.docs || [];
        ragDocsCache.kbId = kbId;
        ragDocsCache.page = 1;
        ragDocsCache.filter = '';
        renderRagDocList();
    } catch(e) { div.innerHTML = '<div style="color:#dc3545;">加载失败</div>'; }
}


// ==================== FAQ Management ====================

async function loadRagFaqKbSelect() {
    try {
        var res = await ragFetch('/api/rag/kb/list');
        var data = await res.json();
        var kbs = data.kbs || [];
        var sel = document.getElementById('ragFaqKbSelect');
        sel.innerHTML = '<option value="">选择板块</option>';
        for (var i = 0; i < kbs.length; i++) {
            sel.innerHTML += '<option value="' + kbs[i].id + '">' + escHtml(kbs[i].name) + '</option>';
        }
    } catch(e) {}
}

async function loadRagFaqs() {
    var kbId = document.getElementById('ragFaqKbSelect').value;
    var div = document.getElementById('ragAdminFaqList');
    if (!kbId) { div.innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">请先选择板块</div>'; return; }
    try {
        var res = await ragFetch('/api/rag/faq/list?kb_id=' + kbId);
        var data = await res.json();
        var faqs = data.faqs || [];
        if (faqs.length === 0) {
            div.innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">暂无FAQ，在上方添加</div>';
            return;
        }
        var html = '';
        for (var i = 0; i < faqs.length; i++) {
            html += '<div style="background:#fff;border:1px solid #e5e7eb;border-radius:8px;padding:10px 14px;margin-bottom:4px;display:flex;gap:10px;align-items:flex-start;">' +
                '<div style="flex:1;">' +
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#0284c7;font-weight:500;">Q: ' + escHtml(faqs[i].question) + '</div>' +
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#374151;margin-top:4px;">A: ' + escHtml(faqs[i].answer) + '</div>' +
                '<div style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;margin-top:4px;">命中: ' + (faqs[i].hit_count||0) + ' 次</div>' +
                '</div>' +
                '<button onclick="ragDeleteFaq(' + faqs[i].id + ')" style="background:none;border:1px solid #fee2e2;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#dc2626;padding:2px 6px;flex-shrink:0;">删</button>' +
                '</div>';
        }
        div.innerHTML = html;
    } catch(e) {}
}

async function ragCreateFaq() {
    var kbId = document.getElementById('ragFaqKbSelect').value;
    var question = document.getElementById('ragFaqQuestion').value.trim();
    var answer = document.getElementById('ragFaqAnswer').value.trim();
    if (!kbId || !question || !answer) { alert('请填写完整'); return; }
    try {
        var res = await ragFetch('/api/rag/faq/create', {
            method: 'POST', headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({kb_id: parseInt(kbId), question: question, answer: answer})
        });
        var data = await res.json();
        if (data.ok) {
            document.getElementById('ragFaqQuestion').value = '';
            document.getElementById('ragFaqAnswer').value = '';
            loadRagFaqs();
        } else { alert('添加失败: ' + (data.error || '')); }
    } catch(e) { alert('添加失败: ' + e.message); }
}

async function ragDeleteFaq(id) {
    if (!confirm('删除此FAQ？')) return;
    try {
        await ragFetch('/api/rag/faq/' + id, { method: 'DELETE' });
        loadRagFaqs();
    } catch(e) {}
}

function renderRagDocList() {
    var div = document.getElementById('ragAdminDocsList');
    var all = ragDocsCache.all;
    var kw = ragDocsCache.filter.toLowerCase();
    var filtered = all;
    if (kw) {
        filtered = all.filter(function(d){ return (d.source_doc||'').toLowerCase().indexOf(kw) >= 0; });
    }

    if (filtered.length === 0) {
        div.innerHTML = '<div style="text-align:center;padding:24px;color:#9ca3af;">' + (kw ? '无匹配文档' : '暂无文档，上传文件开始') + '</div>';
        return;
    }

    var page = ragDocsCache.page;
    var ps = ragDocsCache.pageSize;
    var totalPages = Math.ceil(filtered.length / ps);
    if (page > totalPages) page = totalPages;
    var start = (page-1) * ps;
    var pageDocs = filtered.slice(start, start + ps);

    var html = '';
    // Search + stats bar
    // Batch actions bar
    var selCount = ragDocsCache.selected ? ragDocsCache.selected.length : 0;
    html += '<div style="display:flex;gap:8px;align-items:center;margin-bottom:10px;flex-wrap:wrap;">' +
        '<input placeholder="搜索文档名..." value="' + escHtml(ragDocsCache.filter) + '" oninput="ragDocsCache.filter=this.value;ragDocsCache.page=1;renderRagDocList()" ' +
        'style="flex:1;padding:6px 10px;border:1px solid #e5e7eb;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));min-width:140px;">' +
        '<span style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#9ca3af;white-space:nowrap;">共 ' + filtered.length + ' 个</span>' +
        (selCount > 0 ? '<button onclick="ragBatchDelete()" style="padding:5px 12px;background:#dc2626;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));">删除选中(' + selCount + ')</button>' : '') +
        '</div>';

    // Select all checkbox
    var allSelected = pageDocs.every(function(d){ return (ragDocsCache.selected||[]).indexOf(d.source_doc) >= 0; });
    html += '<div style="margin-bottom:4px;">' +
        '<label style="display:flex;align-items:center;gap:6px;padding:4px 0;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;cursor:pointer;">' +
        '<input type="checkbox" onchange="ragToggleSelectAll(this.checked,' + JSON.stringify(pageDocs.map(function(d){ return d.source_doc; })) + ')" ' + (allSelected ? 'checked' : '') + ' style="width:14px;height:14px;"> 全选本页</label></div>';

    // Doc list
    for (var i = 0; i < pageDocs.length; i++) {
        var d = pageDocs[i];
        var kbId = ragDocsCache.kbId;
        var checked = (ragDocsCache.selected||[]).indexOf(d.source_doc) >= 0;
        html += '<div style="display:flex;align-items:center;gap:8px;padding:7px 10px;background:#fff;border:1px solid ' + (checked ? '#0284c7' : '#e5e7eb') + ';border-radius:8px;margin-bottom:3px;">' +
            '<input type="checkbox" onchange="ragToggleSelect(\''+escHtml(d.source_doc)+'\')" ' + (checked ? 'checked' : '') + ' style="width:14px;height:14px;flex-shrink:0;">' +
            '<div style="flex:1;min-width:0;display:flex;align-items:center;gap:8px;">' +
            '<div style="min-width:0;">' +
            '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#374151;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">' + escHtml(d.source_doc) + '</div>' +
            '<div style="font-size:calc(11px * var(--macs-font-scale, 1));color:#9ca3af;">' + (d.chunks||0) + ' 块</div>' +
            '</div>' +
            '<button onclick="ragViewChunks('+kbId+',\''+escHtml(d.source_doc)+'\')" style="background:none;border:1px solid #e5e7eb;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));color:#0284c7;padding:2px 6px;flex-shrink:0;white-space:nowrap;">查看</button>' +
            (d.chunks === 0 ? '<button onclick="ragRechunkDoc('+kbId+',\''+escHtml(d.source_doc)+'\')" style="background:none;border:1px solid #fde68a;border-radius:4px;cursor:pointer;font-size:calc(11px * var(--macs-font-scale, 1));color:#d97706;padding:2px 6px;flex-shrink:0;white-space:nowrap;">重试</button>' : '') +
            '</div>' +
            '<button onclick="ragSingleDelete('+kbId+',\''+escHtml(d.source_doc)+'\')" style="background:none;border:1px solid #fee2e2;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#dc2626;padding:2px 6px;flex-shrink:0;">删</button>' +
            '</div>';
    }

    // Pagination
    if (totalPages > 1) {
        html += '<div style="display:flex;gap:4px;justify-content:center;margin-top:10px;">';
        html += '<button onclick="ragDocsCache.page=Math.max(1,ragDocsCache.page-1);renderRagDocList()" ' +
            'style="padding:4px 10px;border:1px solid #e5e7eb;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;background:#fff;" ' +
            (page <= 1 ? 'disabled' : '') + '>◀</button>';
        html += '<span style="padding:4px 8px;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#374151;">' + page + ' / ' + totalPages + '</span>';
        html += '<button onclick="ragDocsCache.page=Math.min('+totalPages+',ragDocsCache.page+1);renderRagDocList()" ' +
            'style="padding:4px 10px;border:1px solid #e5e7eb;border-radius:4px;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#6b7280;background:#fff;" ' +
            (page >= totalPages ? 'disabled' : '') + '>▶</button>';
        html += '</div>';
    }

    div.innerHTML = html;
}

var ragChunksCache = {};
async function ragViewChunks(kbId, filename) {
    var key = kbId + '/' + filename;
    var panel = document.getElementById('ragChunkPanel');
    var listArea = document.getElementById('ragAdminDocsList');

    // Toggle off if already showing
    if (ragChunksCache[key] && panel.style.display !== 'none') {
        panel.style.display = 'none';
        delete ragChunksCache[key];
        return;
    }

    try {
        var res = await ragFetch('/api/rag/kb/' + kbId + '/chunks?doc=' + encodeURIComponent(filename));
        var data = await res.json();
        var chunks = data.chunks || [];
        if (chunks.length === 0) { alert('暂无分块'); return; }
        ragChunksCache[key] = chunks;

        var html = '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:10px;">' +
            '<b style="font-size:calc(14.3px * var(--macs-font-scale, 1));color:#1e293b;">' + escHtml(filename) + ' — ' + chunks.length + ' 个分块</b>' +
            '<span onclick="document.getElementById(\'ragChunkPanel\').style.display=\'none\'" style="cursor:pointer;color:#9ca3af;font-size:calc(17.6px * var(--macs-font-scale, 1));">&times;</span>' +
            '</div>' +
            '<div style="max-height:55vh;overflow-y:auto;">';
        for (var i = 0; i < chunks.length; i++) {
            html += '<div style="background:#f8fafc;border:1px solid #e5e7eb;border-radius:6px;padding:8px 12px;margin-bottom:6px;">' +
                '<div style="font-weight:600;font-size:calc(12.1px * var(--macs-font-scale, 1));color:#0284c7;margin-bottom:4px;">#' + (chunks[i].chunk_index+1) + ' (' + (chunks[i].content||'').length + ' 字符)</div>' +
                '<div style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#374151;line-height:1.7;white-space:pre-wrap;word-break:break-all;">' + escHtml((chunks[i].content||'').substring(0,500)) + '</div>' +
                '</div>';
        }
        html += '</div>';
        panel.innerHTML = html;
        panel.style.display = '';
        listArea.after(panel);
    } catch(e) { alert('加载失败: ' + e.message); }
}

ragDocsCache.selected = [];

function ragToggleSelect(filename) {
    var s = ragDocsCache.selected || [];
    var idx = s.indexOf(filename);
    if (idx >= 0) s.splice(idx, 1); else s.push(filename);
    ragDocsCache.selected = s;
    renderRagDocList();
}

function ragToggleSelectAll(checked, names) {
    var s = ragDocsCache.selected || [];
    for (var i = 0; i < names.length; i++) {
        var idx = s.indexOf(names[i]);
        if (checked && idx < 0) s.push(names[i]);
        if (!checked && idx >= 0) s.splice(idx, 1);
    }
    ragDocsCache.selected = s;
    renderRagDocList();
}

async function ragBatchDelete() {
    var s = ragDocsCache.selected || [];
    if (s.length === 0) return;
    if (!confirm('确认删除选中的 ' + s.length + ' 个文档？此操作不可恢复。')) return;
    var kbId = ragDocsCache.kbId;
    var failed = 0;
    for (var i = 0; i < s.length; i++) {
        try {
            await ragFetch('/api/rag/doc/' + kbId + '/' + encodeURIComponent(s[i]), { method: 'DELETE' });
        } catch(e) { failed++; }
    }
    ragDocsCache.selected = [];
    loadRagDocs();
    loadRagKbList();
}

async function ragRechunkDoc(kbId, filename) {
    var listDiv = document.getElementById('ragAdminDocsList');
    listDiv.innerHTML = '<div style="text-align:center;padding:24px;color:#0284c7;">正在重新解析 ' + escHtml(filename) + '...</div>';
    try {
        var res = await ragFetch('/api/rag/kb/' + kbId + '/rechunk?doc=' + encodeURIComponent(filename), { method: 'POST' });
        var data = await res.json();
        if (data.ok) {
            listDiv.innerHTML = '<div style="text-align:center;padding:16px;color:#22c55e;">✅ 重新解析完成 — ' + (data.chunks||0) + ' 个分块</div>';
        } else {
            listDiv.innerHTML = '<div style="text-align:center;padding:24px;color:#dc3545;">❌ 失败: ' + (data.error || '未知') + '</div>';
        }
    } catch(e) {
        listDiv.innerHTML = '<div style="text-align:center;padding:24px;color:#dc3545;">❌ 失败: ' + e.message + '</div>';
    }
    loadRagKbList();
}

async function ragSingleDelete(kbId, filename) {
    if (!confirm('删除 ' + filename + '？')) return;
    await ragFetch('/api/rag/doc/' + kbId + '/' + encodeURIComponent(filename), { method: 'DELETE' });
    ragDocsCache.all = ragDocsCache.all.filter(function(d){ return d.source_doc !== filename; });
    renderRagDocList();
    loadRagKbList();
}

async function ragDeleteDoc(kbId, filename) {
    try {
        var res = await ragFetch('/api/rag/doc/' + kbId + '/' + encodeURIComponent(filename), { method: 'DELETE' });
        var data = await res.json();
        if (data.ok) {
            ragDocsCache.all = ragDocsCache.all.filter(function(d){ return d.source_doc !== filename; });
            renderRagDocList();
            loadRagKbList();
        } else { alert('删除失败: ' + (data.error || '')); }
    } catch(e) { alert('删除失败: ' + e.message); }
}

async function ragUploadDoc() {
    var input = document.getElementById('ragAdminFileInput');
    var kbId = document.getElementById('ragDocKbSelect').value;
    if (!kbId) { alert('请先在板块管理中选择或创建一个知识库板块'); return; }
    var files = input.files;
    if (!files.length) return;

    var listDiv = document.getElementById('ragAdminDocsList');
    var total = files.length;
    for (var i = 0; i < files.length; i++) {
        var f = files[i];
        // Show progress
        listDiv.innerHTML = '<div style="text-align:center;padding:24px;color:#0284c7;">' +
            '<div style="font-size:calc(22px * var(--macs-font-scale, 1));margin-bottom:8px;">📤</div>' +
            '<div>正在上传: ' + escHtml(f.name) + ' (' + (i+1) + '/' + total + ')</div>' +
            '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#9ca3af;margin-top:4px;">提取文本并分块中...</div>' +
            '</div>';

        var form = new FormData();
        form.append('file', f);
        form.append('kb_id', kbId);
        try {
            var res = await ragFetch('/api/rag/doc/upload', { method: 'POST', body: form });
            var data = await res.json();
            if (!data.ok) {
                listDiv.innerHTML = '<div style="text-align:center;padding:24px;color:#dc3545;">❌ ' + escHtml(f.name) + ': ' + (data.error || '上传失败') + '</div>';
                return;
            }
            listDiv.innerHTML = '<div style="text-align:center;padding:16px;color:#22c55e;">✅ ' + escHtml(f.name) + ' — ' + (data.chunks||0) + ' 个分块</div>';
        } catch(e) {
            listDiv.innerHTML = '<div style="text-align:center;padding:24px;color:#dc3545;">❌ ' + escHtml(f.name) + ': ' + e.message + '</div>';
            return;
        }
    }
    setTimeout(function(){ loadRagDocs(); loadRagKbList(); }, 800);
    input.value = '';
}

// ==================== RAG Chat ====================

var ragMessages = [];

async function ragSend() {
    var input = document.getElementById('ragInput');
    var q = input.value.trim();
    if (!q) return;
    input.value = '';
    input.style.height = 'auto';

    ragMessages.push({type:'user',content:q});
    renderRagMessages();

    ragMessages.push({type:'assistant',content:'<span style="color:#9ca3af;">检索中...</span>',loading:true});
    renderRagMessages();

    var kbId = document.getElementById('ragKbSelect').value;
    var body = {query: q, top_k: 5};
    if (kbId) body.kb_ids = [parseInt(kbId)];

    var results = [];
    try {
        var res = await ragFetch('/api/rag/chat', {
            method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)
        });
        var data = await res.json();
        results = data.results || [];
    } catch(e) { console.error('rag chat', e); }

    ragMessages = ragMessages.filter(function(m){ return !m.loading; });

    if (results.length === 0) {
        var healthEl = document.getElementById('ragBleveStatus');
        var healthText = healthEl ? healthEl.textContent : '';
        var isBleveZero = healthText.indexOf('Bleve 0') >= 0;
        var hint;
        if (isBleveZero) {
            hint = ' Bleve索引为空！请到 RAG管理面板点「同步」按钮重建索引。';
        } else {
            hint = ' 未找到匹配内容，尝试换几个关键词、或用空格分隔多个词。';
        }
        ragMessages.push({type:'assistant',content:'未找到相关内容。' + hint});
    } else {
        var tokens = data.tokens || '';
        var content = results.map(function(r,i){
            var snippet = r.content.substring(0,400);
            // Highlight matched tokens
            if (tokens) {
                snippet = highlightTokens(snippet, tokens);
            }
            var matchInfo = '';
            if (r.matched_tokens && r.matched_tokens.length > 0) {
                matchInfo = ' <span style="font-size:calc(11px * var(--macs-font-scale, 1));color:#0284c7;background:#dbeafe;padding:1px 6px;border-radius:3px;margin-left:4px;">命中: ' + r.matched_tokens.join(', ') + '</span>';
            }
            return '<b>' + (i+1) + '.</b> ' + snippet + matchInfo +
                '<div class="rag-source">📄 ' + escHtml(r.source_doc) +
                ' <span class="rag-score-tag ' + (r.score >= 2 ? 'high' : (r.score >= 0.5 ? 'med' : 'low')) + '">' + Math.abs(r.score).toFixed(1) + '</span></div>';
        }).join('<br><br>');
        var kbName = document.getElementById('ragKbSelect').selectedOptions[0] ? document.getElementById('ragKbSelect').selectedOptions[0].text : '全部';
        var metaText = '板块: ' + kbName + ' | 找到 ' + results.length + ' 条';
        if (tokens) { metaText += ' | 分词: ' + tokens; }
        ragMessages.push({type:'assistant',content:content,meta:metaText});
    }
    renderRagMessages();
}

function renderRagMessages() {
    var div = document.getElementById('ragMessages');
    var html = '';
    for (var i = 0; i < ragMessages.length; i++) {
        var m = ragMessages[i];
        html += '<div class="rag-msg ' + m.type + '">' +
            '<div class="rag-avatar">' + (m.type === 'user' ? 'U' : 'AI') + '</div>' +
            '<div class="rag-bubble">' + m.content +
            (m.meta ? '<div class="rag-source" style="margin-top:6px;">' + escHtml(m.meta) + '</div>' : '') +
            '</div></div>';
    }
    div.innerHTML = html;
    div.scrollTop = div.scrollHeight;
}

function ragClear() {
    ragMessages = [];
    document.getElementById('ragMessages').innerHTML =
        '<div class="rag-msg assistant"><div class="rag-avatar">AI</div><div class="rag-bubble">已清屏，请输入检索问题。</div></div>';
}


function highlightTokens(text, tokens) {
    if (!tokens || !text) return escHtml(text);
    // Build keyword list: longest first to avoid partial overlaps
    var kws = tokens.split(/\s+/).filter(function(k){ return k.length >= 2; });
    kws.sort(function(a,b){ return b.length - a.length; });
    if (kws.length === 0) return escHtml(text);

    // Escape text for HTML first
    var html = escHtml(text);

    // Build regex from keywords
    var escaped = kws.map(function(k){
        return k.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    });
    var re = new RegExp('(' + escaped.join('|') + ')', 'gi');
    html = html.replace(re, '<mark>$1</mark>');
    return html;
}

function escHtml(s) {
    if (!s) return '';
    return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}


async function ragPreviewDoc(kbId, filename) {
    try {
        var resp = await ragFetch("/api/rag/doc/preview?kb_id=" + kbId + "&doc=" + encodeURIComponent(filename));
        var data = await resp.json();
        if (data.ok) {
            var modal = document.createElement("div");
            modal.className = "mac-modal-backdrop";
            modal.style.cssText = "position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,0.5);z-index:9999;display:flex;align-items:center;justify-content:center;";
            modal.onclick = function(e){ if(e.target===modal) modal.remove(); };
            modal.innerHTML = "<div style=\"background:#fff;border-radius:12px;padding:24px;max-width:800px;max-height:80vh;overflow-y:auto;width:90vw;\"><h3 style=\"margin:0 0 12px;\">" + esc(data.filename) + "</h3><pre style=\"white-space:pre-wrap;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;color:#374151;background:#f9fafb;padding:16px;border-radius:8px;\">" + esc(data.content) + "</pre><button onclick=\"this.parentElement.parentElement.remove()\" style=\"margin-top:12px;padding:8px 16px;background:#0284c7;color:#fff;border:none;border-radius:6px;cursor:pointer;\">关闭</button></div>";
            document.body.appendChild(modal);
        }
    } catch(e) { alert("预览失败: " + e.message); }
}

// CSV FAQ upload
function ragUploadCSV() {
    var kbId = document.getElementById("ragFaqKbSelect")?.value;
    if (!kbId) { alert("请选择知识库"); return; }
    var input = document.createElement("input");
    input.type = "file"; input.accept = ".csv";
    input.onchange = async function() {
        var fd = new FormData();
        fd.append("file", input.files[0]);
        fd.append("kb_id", kbId);
        try {
            var resp = await ragFetch("/api/rag/faq/import-csv", {method:"POST",body:fd});
            var data = await resp.json();
            if (data.ok) { loadRagFaqs(); alert("已导入 " + data.imported + " 条 FAQ"); }
        } catch(e) { alert("导入失败: " + e.message); }
    };
    input.click();
}

// Search history panel
function loadSearchHistory() {
    var kbId = document.getElementById("ragFaqKbSelect")?.value || "";
    ragFetch("/api/rag/search-log?kb_id=" + kbId + "&limit=30").then(r=>r.json()).then(function(d){
        var html = "<table style=\"width:100%;border-collapse:collapse;font-size:calc(13.2px * var(--macs-font-scale, 1));\"><thead><tr style=\"text-align:left;border-bottom:2px solid #e5e7eb;\"><th style=\"padding:6px;\">时间</th><th style=\"padding:6px;\">查询</th><th style=\"padding:6px;\">命中数</th><th style=\"padding:6px;\">反馈</th></tr></thead><tbody>";
        (d.logs||[]).forEach(function(l){
            html += "<tr style=\"border-bottom:1px solid #f0f0f0;\"><td style=\"padding:6px;color:#9ca3af;\">" + (l.created_at||"").substring(11,16) + "</td><td style=\"padding:6px;max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;\">" + esc(l.query) + "</td><td style=\"padding:6px;\">" + (l.hit_count||0) + "</td><td style=\"padding:6px;\">" + (l.feedback||"-") + "</td></tr>";
        });
        document.getElementById("searchHistoryContent").innerHTML = html + "</tbody></table>";
    });
}
