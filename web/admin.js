// ====================== Admin Panel ======================
function escHtml(s){if(!s)return'';return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;')}
var adminToken = sessionStorage.getItem('admin_token');

function adminLogin() {
    var pass = document.getElementById('adminPass').value;
    fetch('/api/admin/login', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({password: pass})
    }).then(r => r.json()).then(d => {
        if (d.ok) {
            adminToken = d.token;
            sessionStorage.setItem('admin_token', adminToken);
            document.getElementById('loginOverlay').style.display = 'none';
            switchAdminTab('dashboard');
        } else { alert('密码错误'); }
    });
}

if (adminToken) {
    fetch('/api/admin/verify', {headers:{'X-Admin-Token':adminToken}})
        .then(r => r.json()).then(d => {
            if (d.ok) { document.getElementById('loginOverlay').style.display = 'none'; switchAdminTab('dashboard'); }
            else { sessionStorage.removeItem('admin_token'); adminToken = null; }
        });
}

function adminFetch(url, opts) {
    opts = opts || {};
    opts.headers = opts.headers || {};
    opts.headers['X-Admin-Token'] = adminToken || '';
    return fetch(url, opts);
}

function switchAdminTab(tab) {
    var links = document.querySelectorAll('.admin-sidebar a');
    links.forEach(function(a, i) { a.classList.toggle('active', i <= 5 && links[i].textContent.includes(tab2label(tab))); });
    window['loadAdmin_'+tab]();
}

function tab2label(t) {
    return {dashboard:'仪表盘',knowledge:'知识库',pipelines:'工作流',deployees:'AI员工',tenants:'租户',settings:'设置'}[t]||t;
}

// ===== Dashboard =====
function loadAdmin_dashboard() {
    Promise.all([
        adminFetch('/api/monitor/cache').then(r=>r.json()),
        adminFetch('/api/pipeline/list').then(r=>r.json()),
        adminFetch('/api/knowledge/list').then(r=>r.json()),
        adminFetch('/api/tasks').then(r=>r.json())
    ]).then(function(vals) {
        var cache=vals[0], pipes=vals[1], kb=vals[2], tasks=vals[3];
        document.getElementById('adminContent').innerHTML =
            '<h2 style="margin-bottom:20px;">📊 仪表盘</h2>'+
            '<div class="admin-stat">'+
                '<div class="admin-stat-card"><div class="num">'+((cache&&cache.cache_hit_rate)||0).toFixed(1)+'%</div><div class="label">缓存命中率</div></div>'+
                '<div class="admin-stat-card"><div class="num">'+((pipes&&pipes.pipelines)||[]).length+'</div><div class="label">工作流</div></div>'+
                '<div class="admin-stat-card"><div class="num">'+((kb&&kb.entries)||[]).length+'</div><div class="label">知识条目</div></div>'+
                '<div class="admin-stat-card"><div class="num">'+((tasks&&tasks.tasks)||[]).length+'</div><div class="label">活跃任务</div></div>'+
            '</div>'+
            '<div class="admin-card"><h3>近期 LLM 调用</h3><div id="recentCalls" style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;">加载中...</div></div>';
        if (cache && cache.recent) {
            var html = '<table class="admin-table"><thead><tr><th>时间</th><th>模型</th><th>Agent</th><th>Token</th><th>命中</th><th>耗时</th></tr></thead><tbody>';
            for (var i=0;i<Math.min(cache.recent.length,10);i++) {
                var r=cache.recent[i];
                html += '<tr><td>'+r.time+'</td><td>'+r.model+'</td><td>'+r.agent_name+'</td><td>'+r.prompt_tokens+'</td><td>'+((r.cache_hit_tokens/r.prompt_tokens*100)||0).toFixed(0)+'%</td><td>'+r.latency_ms+'ms</td></tr>';
            }
            html += '</tbody></table>';
            document.getElementById('recentCalls').innerHTML = html;
        }
    });
}

// ===== Knowledge =====
function loadAdmin_knowledge() {
    adminFetch('/api/knowledge/list').then(r=>r.json()).then(data => {
        var html = '<h2 style="margin-bottom:16px;">📚 知识库管理</h2>';
        html += '<div class="admin-card"><h3>新增条目</h3>'+
            '<input id="akbTitle" placeholder="标题" style="margin-bottom:8px;">'+
            '<textarea id="akbContent" rows="4" placeholder="内容（Markdown）" style="margin-bottom:8px;"></textarea>'+
            '<input id="akbCategory" placeholder="分类" style="margin-bottom:8px;width:200px;">'+
            '<button class="admin-btn admin-btn-success" onclick="adminSaveKB()">保存</button>'+
            '<input type="file" onchange="adminUploadKB()" style="margin-left:12px;">'+
            '</div>';
        html += '<div class="admin-card"><h3>知识条目列表 ('+(data.entries||[]).length+')</h3><table class="admin-table"><thead><tr><th>标题</th><th>分类</th><th>内容预览</th><th>操作</th></tr></thead><tbody>';
        for (var i=0;i<(data.entries||[]).length;i++) {
            var e = data.entries[i];
            html += '<tr><td>'+escHtml(e.title)+'</td><td>'+escHtml(e.category||'')+'</td><td style="max-width:300px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;">'+escHtml((e.summary||'').substring(0,80))+'</td>'+
                '<td><button class="admin-btn admin-btn-danger" onclick="adminDeleteKB('+e.id+')" style="font-size:calc(11px * var(--macs-font-scale, 1));">删除</button></td></tr>';
        }
        html += '</tbody></table></div>';
        document.getElementById('adminContent').innerHTML = html;
    });
}

function adminSaveKB() {
    var body = {title:document.getElementById('akbTitle').value, content:document.getElementById('akbContent').value, category:document.getElementById('akbCategory').value, tags:[]};
    adminFetch('/api/knowledge/save', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}).then(r=>r.json()).then(d=>{
        if(d.ok){alert('已保存');loadAdmin_knowledge();}else{alert('失败: '+(d.error||''));}
    });
}
function adminDeleteKB(id) { if(confirm('确认删除?')){adminFetch('/api/knowledge/'+id,{method:'DELETE'}).then(r=>loadAdmin_knowledge());} }
function adminUploadKB() {
    var file = event.target.files[0]; if(!file) return;
    var form = new FormData(); form.append('file',file);
    adminFetch('/api/knowledge/upload',{method:'POST',body:form}).then(r=>r.json()).then(d=>{if(d.ok)loadAdmin_knowledge();});
}

// ===== Pipelines =====
function loadAdmin_pipelines() {
    adminFetch('/api/pipeline/list').then(r=>r.json()).then(data => {
        var html = '<h2 style="margin-bottom:16px;">🔀 工作流管理</h2>';
        html += '<div class="admin-card"><h3>工作流列表 ('+(data.pipelines||[]).length+')</h3><table class="admin-table"><thead><tr><th>名称</th><th>描述</th><th>步骤数</th><th>操作</th></tr></thead><tbody>';
        for (var i=0;i<(data.pipelines||[]).length;i++) {
            var p = data.pipelines[i];
            html += '<tr><td>'+escHtml(p.name)+'</td><td>'+escHtml(p.desc||p.description||'')+'</td><td>'+(p.steps?p.steps.length:0)+'</td>'+
                '<td><button class="admin-btn admin-btn-danger" onclick="adminDeletePipe(\''+p.name+'\')" style="font-size:calc(11px * var(--macs-font-scale, 1));">删除</button></td></tr>';
        }
        html += '</tbody></table></div>';
        document.getElementById('adminContent').innerHTML = html;
    });
}
function adminDeletePipe(name) { if(confirm('删除工作流: '+name+'?')){adminFetch('/api/pipeline/save',{method:'DELETE',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:name})}).then(r=>loadAdmin_pipelines());} }

// ===== DE =====
// AI员工 = ae_panel.js (single source of truth)
function loadAdmin_deployees() {
    document.getElementById('adminContent').innerHTML = '<div id="aeListContent"></div>';
    if (typeof loadAEList === 'function') loadAEList();
}

function loadAdmin_tenants() {
    document.getElementById('adminContent').innerHTML = '<h2 style="margin-bottom:16px;">🏢 租户管理</h2><div class="admin-card"><p style="color:#94a3b8;">租户配置在 config.yaml 的 tenants 段。每个租户有独立的工作目录。</p></div>';
}
function loadAdmin_settings() {
    adminFetch('/api/admin/license').then(r=>r.json()).then(data => {
        var html = '<h2 style="margin-bottom:16px;">⚙️ 授权管理</h2>';
        html += '<div class="admin-card"><h3>生成授权文件</h3>'+
            '<input id="licTenant" placeholder="租户ID" style="margin-bottom:8px;width:200px;">'+
            '<input id="licUsers" type="number" value="50" placeholder="最大用户数" style="margin-bottom:8px;width:150px;">'+
            '<input id="licExpires" type="date" value="2027-06-13" style="margin-bottom:8px;width:180px;">'+
            '<div><button class="admin-btn admin-btn-success" onclick="genLicense()">生成授权文件</button></div>'+
            '</div>';
        html += '<div class="admin-card"><h3>现有授权 ('+(data.licenses||[]).length+')</h3><table class="admin-table"><thead><tr><th>租户</th><th>最大用户</th><th>到期</th></tr></thead><tbody>';
        for (var i=0;i<(data.licenses||[]).length;i++) {
            var l = data.licenses[i];
            html += '<tr><td>'+escHtml(l.tenant_id)+'</td><td>'+l.max_users+'</td><td>'+l.expires_at+'</td></tr>';
        }
        html += '</tbody></table></div>';
        document.getElementById('adminContent').innerHTML = html;
    });
}
function genLicense() {
    var body = {
        tenant_id: document.getElementById('licTenant').value,
        max_users: parseInt(document.getElementById('licUsers').value)||50,
        expires_at: document.getElementById('licExpires').value
    };
    adminFetch('/api/admin/license', {method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)})
        .then(r=>r.json()).then(d => {
            if (d.ok) { alert('授权文件已生成: '+d.file); loadAdmin_settings(); }
            else { alert('失败: '+(d.error||'')); }
        });
}


// ====================== Hooks / Agents / Plugins Tabs ======================
function loadAdmin_hooks() {
	var el = document.getElementById("adminContent");
	el.innerHTML = '<div class="admin-card"><h3>Hooks Management</h3>'+
	  '<p style="color:#6b7280;font-size:calc(13.2px * var(--macs-font-scale, 1));margin-bottom:12px">Scripts in .openkitty/hooks/: pre_tool_use.sh, post_tool_use.sh, pre_compact.sh, post_compact.sh, session_start.sh, stop.sh</p>'+
	  '<div id="hooksList" style="font-size:calc(13.2px * var(--macs-font-scale, 1));font-family:monospace;background:#f9fafb;padding:12px;border-radius:8px">Loading...</div></div>';
	fetch("/api/chat",{method:"POST",headers:{"Content-Type":"application/json"},
	  body:JSON.stringify({message:"manage_hooks action=list",session_id:"admin-hooks",stream:false})})
	  .then(function(r){return r.json()}).then(function(d){document.getElementById("hooksList").innerText=d.result||"No hooks"});
}

function loadAdmin_agents() {
	var el = document.getElementById("adminContent");
	el.innerHTML = '<div class="admin-card"><h3>Agents Management</h3>'+
	  '<p style="color:#6b7280;font-size:calc(13.2px * var(--macs-font-scale, 1));margin-bottom:12px">YAML/JSON files in .openkitty/agents/</p>'+
	  '<div id="agentsList" style="font-size:calc(14.3px * var(--macs-font-scale, 1));background:#f9fafb;padding:12px;border-radius:8px;white-space:pre-wrap">Loading...</div>'+
	  '<button onclick="reloadAgentsX()" class="admin-btn admin-btn-primary" style="margin-top:8px">Reload Agents</button></div>';
	fetch("/api/chat",{method:"POST",headers:{"Content-Type":"application/json"},
	  body:JSON.stringify({message:"manage_agents action=list",session_id:"admin-agents",stream:false})})
	  .then(function(r){return r.json()}).then(function(d){document.getElementById("agentsList").innerHTML=d.result||"No agents"});
}

function reloadAgentsX() {
	fetch("/api/chat",{method:"POST",headers:{"Content-Type":"application/json"},
	  body:JSON.stringify({message:"manage_agents action=reload",session_id:"admin-agents",stream:false})})
	  .then(function(r){return r.json()}).then(function(d){alert(d.result||"Done");loadAdmin_agents();});
}

function loadAdmin_plugins() {
	var el = document.getElementById("adminContent");
	el.innerHTML = '<div class="admin-card"><h3>Plugins Management</h3>'+
	  '<p style="color:#6b7280;font-size:calc(13.2px * var(--macs-font-scale, 1));margin-bottom:12px">JSON manifests in .openkitty/plugins/</p>'+
	  '<div id="pluginsList" style="font-size:calc(14.3px * var(--macs-font-scale, 1));background:#f9fafb;padding:12px;border-radius:8px;white-space:pre-wrap">Loading...</div>'+
	  '<button onclick="discoverPluginsX()" class="admin-btn admin-btn-success" style="margin-top:8px">Discover Plugins</button></div>';
	fetch("/api/chat",{method:"POST",headers:{"Content-Type":"application/json"},
	  body:JSON.stringify({message:"manage_plugins action=list",session_id:"admin-plugins",stream:false})})
	  .then(function(r){return r.json()}).then(function(d){document.getElementById("pluginsList").innerHTML=d.result||"No plugins"});
}

function discoverPluginsX() {
	fetch("/api/chat",{method:"POST",headers:{"Content-Type":"application/json"},
	  body:JSON.stringify({message:"manage_plugins action=discover",session_id:"admin-plugins",stream:false})})
	  .then(function(r){return r.json()}).then(function(d){alert(d.result||"Done");loadAdmin_plugins();});
}

var _origSwitchAdminTab = switchAdminTab;
switchAdminTab = function(tab) {
	if (tab === "hooks") { loadAdmin_hooks(); return; }
	if (tab === "agents") { loadAdmin_agents(); return; }
	if (tab === "plugins") { loadAdmin_plugins(); return; }
	return _origSwitchAdminTab(tab);
};
