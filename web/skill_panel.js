// ==================== Skill Management ====================
var ownedSkills = [];
var skillEditorOriginal = null;
var skillEditorReturnFocus = null;
var skillEditorSaving = false;
var skillListRequest = 0;
var ownedSkillCategory = 'all';
var ownedSkillCategories = [
    {id: 'all', key: 'skillCategoryAll', label: '全部'},
    {id: 'office', key: 'skillCategoryOffice', label: '办公文档'},
    {id: 'data', key: 'skillCategoryData', label: '数据分析'},
    {id: 'development', key: 'skillCategoryDevelopment', label: '开发编程'},
    {id: 'research', key: 'skillCategoryResearch', label: '搜索研究'},
    {id: 'creative', key: 'skillCategoryCreative', label: '设计创作'},
    {id: 'business', key: 'skillCategoryBusiness', label: '商务运营'},
    {id: 'finance', key: 'skillCategoryFinance', label: '投资金融'},
    {id: 'automation', key: 'skillCategoryAutomation', label: '自动化运维'},
    {id: 'life', key: 'skillCategoryLife', label: '生活健康'},
    {id: 'assistant', key: 'skillCategoryAssistant', label: '助手管理'},
    {id: 'other', key: 'skillCategoryOther', label: '其他'}
];

function skillPanelText(key, fallback) {
    return typeof window.openKittyT === 'function' ? window.openKittyT(key, fallback) : fallback;
}

function openSkillPanel() {
    ownedSkillCategory = 'all';
    var skillText = typeof window.openKittyT === 'function' ? window.openKittyT : function(key, fallback) { return fallback; };
    var overlay = document.getElementById('pipelinePanel');
    if (!overlay) return;
    overlay.style.display = 'flex';
    var views = overlay.querySelectorAll('.pipeline-view');
    views.forEach(function(v){v.style.display='none';});
    var detailView = document.getElementById('pipelineDetailView');
    if (!detailView) return;
    detailView.style.display = 'block';
    var backBtn = document.getElementById('detailBackBtn');
    if (backBtn) backBtn.style.display = 'none';
    var titleH2 = overlay.querySelector('.pipeline-title h2');
    if (titleH2) titleH2.textContent = skillText('mySkillsTitle', '我的 Skill');
    var tabs = overlay.querySelector('.pipeline-tabs');
    if (tabs) tabs.style.display = 'none';

    var html = '<p class="config-hint" style="margin:0 0 16px;">'+escHtml(skillText('mySkillsHint', 'Skill 包含专业知识、方法和操作流程，可查看、编辑或安装。'))+'</p>'+
        '<div class="owned-skills-toolbar">'+
        '<button type="button" onclick="showSkillForm()" class="settings-action-btn primary">+ '+escHtml(skillText('skillNew', '新建技能'))+'</button>'+
        '<button type="button" onclick="showSkillInstallForm()" class="settings-action-btn">'+escHtml(skillText('skillInstall', '安装技能'))+'</button>'+
        '<button type="button" onclick="skillReload()" class="settings-action-btn">'+escHtml(skillText('skillReload', '重新加载'))+'</button>'+
        '<button type="button" onclick="loadSkillList()" class="settings-action-btn">'+escHtml(skillText('skillRefresh', '刷新'))+'</button>'+
        '<input id="ownedSkillSearch" class="owned-skills-search" type="search" oninput="renderOwnedSkills()" placeholder="'+escHtml(skillText('skillSearch', '搜索技能名称或说明…'))+'" aria-label="'+escHtml(skillText('skillSearch', '搜索技能名称或说明…'))+'">'+
        '</div>'+
        '<div id="skillInstallForm" style="display:none;margin-bottom:12px;padding:14px;background:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;">'+
        '<div style="font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:700;color:#334155;margin-bottom:9px;">在线 Skill 市场</div>'+
        '<div style="display:flex;gap:7px;align-items:center;flex-wrap:wrap;">'+
        '<select id="remoteSkillSource" style="padding:8px;border:1px solid #d1d5db;border-radius:6px;font-size:calc(13.2px * var(--macs-font-scale, 1));background:#fff;">'+
        '<option value="tencent">腾讯 SkillHub（国内优先）</option>'+
        '<option value="clawhub">OpenClaw / ClawHub</option>'+
        '</select>'+
        '<input id="remoteSkillQuery" placeholder="搜索 Skill，例如 robot、ROS2、serial" onkeydown="if(event.key===\'Enter\')skillRemoteSearch()" style="flex:1;min-width:190px;padding:8px;border:1px solid #d1d5db;border-radius:6px;font-size:calc(14.3px * var(--macs-font-scale, 1));">'+
        '<button onclick="skillRemoteSearch()" style="padding:8px 18px;background:#10b981;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));font-weight:600;">搜索</button>'+
        '<button onclick="document.getElementById(\'skillInstallForm\').style.display=\'none\'" style="padding:8px 14px;background:#f1f5f9;border:1px solid #e2e8f0;border-radius:6px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">关闭</button>'+
        '</div>'+
        '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#64748b;margin-top:8px;line-height:1.55;">只下载并校验 <code>SKILL.md</code>，不会下载或执行远端脚本、二进制和依赖。安装位置：<code id="remoteSkillInstallDir">启动文件目录/.openkitty/skills</code></div>'+
        '<div id="remoteSkillStatus" style="margin-top:8px;font-size:calc(13.2px * var(--macs-font-scale, 1));"></div>'+
        '<div id="remoteSkillResults" style="margin-top:8px;max-height:320px;overflow-y:auto;"></div>'+
        '<details style="margin-top:12px;border-top:1px solid #e2e8f0;padding-top:10px;">'+
        '<summary style="font-size:calc(13.2px * var(--macs-font-scale, 1));color:#64748b;cursor:pointer;">高级：从 Git URL 或本地目录安装</summary>'+
        '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#64748b;margin:9px 0 7px;">输入 Git URL 或本地目录路径，选择安装方式</div>'+
        '<input id="skillSourceInput" placeholder="https://github.com/xxx/skill.git  或  E:/my-skills  或  ./my-skills" style="width:100%;padding:8px;border:1px solid #d1d5db;border-radius:6px;font-size:calc(14.3px * var(--macs-font-scale, 1));margin-bottom:8px;">'+
        '<div style="display:flex;gap:6px;flex-wrap:wrap;">'+
        '<button onclick="skillInstallSimple()" style="padding:7px 18px;background:#8b5cf6;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">📋 简单安装</button>'+
        '<button onclick="skillInstallMerge()" style="padding:7px 18px;background:#ec4899;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:calc(14.3px * var(--macs-font-scale, 1));">🧠 智能融合</button>'+
        '</div>'+
        '<div id="skillInstallResult" style="margin-top:8px;font-size:calc(12.1px * var(--macs-font-scale, 1));max-height:200px;overflow-y:auto;"></div>'+
        '</details>'+
        '</div>'+
        '<div id="ownedSkillCategories" class="owned-skills-categories" role="group" aria-label="'+escHtml(skillText('skillCategories', '按用途筛选'))+'"></div>'+
        '<div id="ownedSkillCount" class="owned-skills-count" aria-live="polite"></div>'+
        '<div id="skillList" class="owned-skills-grid" aria-busy="true"></div>';
    document.getElementById('detailContent').innerHTML = html;
    loadSkillList();
}

function showSkillInstallForm() {
    document.getElementById('skillInstallForm').style.display = 'block';
    loadRemoteSkillSources();
    var query = document.getElementById('remoteSkillQuery');
    if (query) query.focus();
}
function showSkillForm(name, content) {
    var modal = ensureSkillEditor();
    skillEditorReturnFocus = document.activeElement;
    document.getElementById('skillEditorTitle').textContent = skillPanelText(name ? 'skillEdit' : 'skillNew', name ? '编辑技能' : '新建技能');
    document.getElementById('skillEditName').value = name || '';
    document.getElementById('skillEditContent').value = typeof content === 'string' ? content : '---\nname: my-skill\ndescription: \n---\n\n# Skill Title\n\n## Usage\n';
    skillEditorOriginal = {name: document.getElementById('skillEditName').value, content: document.getElementById('skillEditContent').value};
    document.getElementById('skillEditorError').textContent = '';
    modal.classList.add('active');
    document.getElementById(name ? 'skillEditContent' : 'skillEditName').focus({preventScroll: true});
}

function ensureSkillEditor() {
    var modal = document.getElementById('skillEditorModal');
    if (modal) return modal;
    modal = document.createElement('div');
    modal.id = 'skillEditorModal';
    modal.className = 'modal skill-editor-overlay';
    modal.setAttribute('role', 'dialog');
    modal.setAttribute('aria-modal', 'true');
    modal.setAttribute('aria-labelledby', 'skillEditorTitle');
    modal.innerHTML = '<div class="modal-content skill-editor-surface">'+
        '<header class="modal-header"><h3 id="skillEditorTitle"></h3><button type="button" class="close-modal" onclick="closeSkillEditor()" aria-label="'+escHtml(skillPanelText('windowClose', '关闭窗口'))+'">&times;</button></header>'+
        '<form id="skillEditForm" class="skill-editor-form" onsubmit="event.preventDefault();skillSave()">'+
        '<p class="skill-editor-hint">'+escHtml(skillPanelText('skillEditorHint', '修改名称与 SKILL.md 内容，保存后应用。'))+'</p>'+
        '<label for="skillEditName">'+escHtml(skillPanelText('skillName', '技能名称'))+'</label><input id="skillEditName" placeholder="my-skill" autocomplete="off" required>'+
        '<label for="skillEditContent">'+escHtml(skillPanelText('skillContent', '技能内容'))+'</label><textarea id="skillEditContent" rows="15" spellcheck="false"></textarea>'+
        '<p id="skillEditorError" class="skill-editor-error" role="alert"></p>'+
        '<footer class="skill-editor-actions"><button type="button" class="settings-action-btn" onclick="closeSkillEditor()">'+escHtml(skillPanelText('skillCancel', '取消'))+'</button><button id="skillEditorSave" type="submit" class="settings-action-btn primary">'+escHtml(skillPanelText('skillSave', '保存技能'))+'</button></footer></form></div>';
    modal.addEventListener('click', function(event) { if (event.target === modal) closeSkillEditor(); });
    modal.addEventListener('keydown', function(event) {
        if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            closeSkillEditor();
        } else if (event.key === 'Tab') {
            var items = Array.from(modal.querySelectorAll('button:not(:disabled), input:not(:disabled), textarea:not(:disabled)')).filter(function(item) { return item.getClientRects().length; });
            var first = items[0], last = items[items.length - 1];
            if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
            else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
        }
    });
    document.body.appendChild(modal);
    return modal;
}

function closeSkillEditor(saved) {
    var modal = document.getElementById('skillEditorModal');
    if (!modal || !modal.classList.contains('active') || skillEditorSaving) return;
    var dirty = skillEditorOriginal && (document.getElementById('skillEditName').value !== skillEditorOriginal.name || document.getElementById('skillEditContent').value !== skillEditorOriginal.content);
    if (!saved && dirty && !confirm(skillPanelText('skillDiscard', '技能有未保存的修改，确定放弃？'))) return;
    modal.classList.remove('active');
    if (skillEditorReturnFocus && skillEditorReturnFocus.isConnected) skillEditorReturnFocus.focus({preventScroll: true});
}

function ownedSkillCategoryInfo(skill) {
    return ownedSkillCategories.find(function(category) { return category.id === skill.category && category.id !== 'all'; }) || ownedSkillCategories[ownedSkillCategories.length - 1];
}

function selectOwnedSkillCategory(category) {
    if (!ownedSkillCategories.some(function(item) { return item.id === category; })) return;
    ownedSkillCategory = category;
    renderOwnedSkills();
    var view = document.getElementById('pipelineDetailView');
    if (view) view.scrollTop = 0;
}

function renderOwnedSkillCategories(matchedSkills) {
    var container = document.getElementById('ownedSkillCategories');
    if (!container) return;
    var counts = {all: ownedSkills.length};
    var matches = {all: matchedSkills.length};
    ownedSkills.forEach(function(skill) {
        var id = ownedSkillCategoryInfo(skill).id;
        counts[id] = (counts[id] || 0) + 1;
    });
    matchedSkills.forEach(function(skill) {
        var id = ownedSkillCategoryInfo(skill).id;
        matches[id] = (matches[id] || 0) + 1;
    });
    if (ownedSkillCategory !== 'all' && !counts[ownedSkillCategory]) ownedSkillCategory = 'all';
    var active = document.activeElement;
    var focusCategory = active && container.contains(active) ? active.dataset.skillCategory : '';
    container.innerHTML = ownedSkillCategories.filter(function(category) { return counts[category.id] || category.id === 'all'; }).map(function(category) {
        var selected = category.id === ownedSkillCategory;
        return '<button type="button" class="owned-skill-category-filter" data-skill-category="'+category.id+'" aria-pressed="'+selected+'" aria-controls="skillList" onclick="selectOwnedSkillCategory(this.dataset.skillCategory)">'+
            '<span>'+escHtml(skillPanelText(category.key, category.label))+'</span><span class="owned-skill-category-count">'+(matches[category.id] || 0)+'</span></button>';
    }).join('');
    if (focusCategory) {
        var target = container.querySelector('[data-skill-category="'+focusCategory+'"]');
        if (target) target.focus({preventScroll: true});
    }
}

function renderOwnedSkills() {
    var container = document.getElementById('skillList');
    if (!container) return;
    var search = document.getElementById('ownedSkillSearch');
    var query = search ? search.value.trim().toLowerCase() : '';
    var matched = ownedSkills.filter(function(s) {
        var category = ownedSkillCategoryInfo(s);
        return [s.name, s.display_name, s.description, s.short_description, (s.aliases || []).join(' '), skillPanelText(category.key, category.label)].join(' ').toLowerCase().indexOf(query) !== -1;
    });
    renderOwnedSkillCategories(matched);
    var skills = matched.filter(function(s) { return ownedSkillCategory === 'all' || ownedSkillCategoryInfo(s).id === ownedSkillCategory; });
    var count = document.getElementById('ownedSkillCount');
    if (count) count.textContent = (query || ownedSkillCategory !== 'all' ? skills.length+' / '+ownedSkills.length : ownedSkills.length)+' '+skillPanelText('skillCount', '个技能');
    container.innerHTML = skills.length ? skills.map(function(s) {
        var name = s.display_name || s.name;
        var desc = s.short_description || s.description || skillPanelText('skillNoDescription', '暂未填写技能说明');
        var source = s.source === 'tencent' ? '腾讯 SkillHub' : (s.source === 'clawhub' ? 'ClawHub' : '');
        var category = ownedSkillCategoryInfo(s);
        return '<article class="owned-skill-card">'+
            '<div class="owned-skill-top"><span class="owned-skill-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 3 3 8l9 5 9-5-9-5ZM3 12l9 5 9-5M3 16l9 5 9-5"/></svg></span>'+(source ? '<span class="owned-skill-source">'+escHtml(source)+'</span>' : '')+'</div>'+
            '<span class="owned-skill-category-label">'+escHtml(skillPanelText(category.key, category.label))+'</span>'+
            '<h3 title="'+escHtml(name)+'">'+escHtml(name)+'</h3><p class="owned-skill-description" title="'+escHtml(desc)+'">'+escHtml(desc)+'</p>'+
            '<code class="owned-skill-name" title="'+escHtml(s.name)+'">'+escHtml(s.name)+'</code>'+
            '<div class="owned-skill-actions"><button type="button" class="settings-action-btn" data-skill-name="'+escHtml(s.name)+'" onclick="skillEdit(this.dataset.skillName)">'+escHtml(skillPanelText('skillEdit', '编辑技能'))+'</button>'+
            '<button type="button" class="owned-skill-delete" data-skill-name="'+escHtml(s.name)+'" onclick="skillDelete(this.dataset.skillName)" aria-label="'+escHtml(skillPanelText('delete', '删除')+' '+name)+'">'+escHtml(skillPanelText('delete', '删除'))+'</button></div></article>';
    }).join('') : '<p class="owned-skills-empty">'+escHtml(skillPanelText(query || ownedSkillCategory !== 'all' ? 'skillNoMatch' : 'skillEmpty', query || ownedSkillCategory !== 'all' ? '没有找到匹配的技能' : '还没有技能，可以新建或安装。'))+'</p>';
}

document.addEventListener('openkitty:languagechange', function() {
    if (document.getElementById('ownedSkillCategories')) renderOwnedSkills();
});

async function loadSkillList() {
    var request = ++skillListRequest;
    var container = document.getElementById('skillList');
    if (!container) return;
    container.setAttribute('aria-busy', 'true');
    try {
        var resp = await fetch('/api/skills');
        var data = await resp.json();
        if (!resp.ok) throw new Error(data.error || resp.status);
        if (request !== skillListRequest || !container.isConnected) return;
        ownedSkills = data.skills || [];
        renderOwnedSkills();
    } catch(e) {
        if (request === skillListRequest && container.isConnected) container.innerHTML = '<p class="owned-skills-empty skill-editor-error">'+escHtml(skillPanelText('skillLoadFailed', '加载失败：')+e.message)+'</p>';
    } finally {
        if (request === skillListRequest) container.setAttribute('aria-busy', 'false');
    }
}

var remoteSkillSearchResults = [];
var remoteSkillSourcesLoaded = false;

async function loadRemoteSkillSources() {
    if (remoteSkillSourcesLoaded) return;
    try {
        var resp = await fetch('/api/skills/sources', {cache:'no-store'});
        var data = await resp.json();
        if (!resp.ok || !data.ok) throw new Error(data.error || '加载市场来源失败');
        var select = document.getElementById('remoteSkillSource');
        if (select && Array.isArray(data.sources) && data.sources.length) {
            select.innerHTML = data.sources.map(function(source) {
                var suffix = source.recommended ? '（国内优先）' : '';
                return '<option value="'+escHtml(source.id)+'">'+escHtml(source.name+suffix)+'</option>';
            }).join('');
        }
        var installDir = document.getElementById('remoteSkillInstallDir');
        if (installDir && data.install_dir) installDir.textContent = data.install_dir;
        remoteSkillSourcesLoaded = true;
    } catch (e) {
        setRemoteSkillStatus(e.message || '加载市场来源失败', true);
    }
}

function setRemoteSkillStatus(message, isError) {
    var status = document.getElementById('remoteSkillStatus');
    if (!status) return;
    status.textContent = message || '';
    status.style.color = isError ? '#dc2626' : '#64748b';
}

async function skillRemoteSearch() {
    var source = document.getElementById('remoteSkillSource').value || 'tencent';
    var query = document.getElementById('remoteSkillQuery').value.trim();
    if (!query) {
        setRemoteSkillStatus('请输入搜索关键词', true);
        return;
    }
    setRemoteSkillStatus('正在搜索…', false);
    document.getElementById('remoteSkillResults').innerHTML = '';
    try {
        var resp = await fetch('/api/skills/search?source='+encodeURIComponent(source)+'&q='+encodeURIComponent(query), {cache:'no-store'});
        var data = await resp.json();
        if (!resp.ok || !data.ok) throw new Error(data.error || '搜索失败');
        remoteSkillSearchResults = data.results || [];
        renderRemoteSkillResults();
        setRemoteSkillStatus(remoteSkillSearchResults.length ? '找到 '+remoteSkillSearchResults.length+' 个 Skill' : '没有找到匹配的 Skill', false);
    } catch (e) {
        remoteSkillSearchResults = [];
        renderRemoteSkillResults();
        setRemoteSkillStatus(e.message || '搜索失败', true);
    }
}

function renderRemoteSkillResults() {
    var container = document.getElementById('remoteSkillResults');
    if (!container) return;
    container.innerHTML = remoteSkillSearchResults.map(function(result, index) {
        var summary = result.summary_zh || result.summary || '暂无说明';
        var source = result.source === 'tencent' ? '腾讯 SkillHub' : 'OpenClaw / ClawHub';
        var meta = [];
        if (result.owner) meta.push('@'+result.owner);
        if (result.version) meta.push('v'+result.version);
        if (result.downloads) meta.push(result.downloads+' 下载');
        var action = result.installed
            ? '<button disabled style="padding:6px 12px;border:none;border-radius:6px;background:#e2e8f0;color:#64748b;font-size:calc(12.1px * var(--macs-font-scale, 1));">已安装</button>'
            : '<button onclick="skillRemoteInstall('+index+')" style="padding:6px 12px;border:none;border-radius:6px;background:#10b981;color:#fff;cursor:pointer;font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:600;">安装</button>';
        return '<div style="background:#fff;border:1px solid #e2e8f0;border-radius:8px;padding:11px;margin-bottom:7px;">'+
            '<div style="display:flex;justify-content:space-between;gap:12px;align-items:flex-start;">'+
            '<div style="min-width:0;">'+
            '<div><strong style="font-size:calc(14.3px * var(--macs-font-scale, 1));">'+escHtml(result.name || result.slug)+'</strong>'+
            '<span style="font-size:calc(9.9px * var(--macs-font-scale, 1));color:#047857;background:#ecfdf5;border-radius:999px;padding:2px 7px;margin-left:6px;">'+escHtml(source)+'</span></div>'+
            '<div style="font-size:calc(12.1px * var(--macs-font-scale, 1));color:#475569;line-height:1.45;margin-top:4px;">'+escHtml(summary)+'</div>'+
            '<div style="font-size:calc(11px * var(--macs-font-scale, 1));color:#94a3b8;margin-top:5px;">'+escHtml(meta.join(' · ') || result.slug)+
            (result.url?' · <a href="'+escHtml(result.url)+'" target="_blank" rel="noopener noreferrer" style="color:#6366f1;">来源页</a>':'')+
            '</div></div>'+action+'</div></div>';
    }).join('');
}

async function skillRemoteInstall(index) {
    var result = remoteSkillSearchResults[index];
    if (!result || result.installed) return;
    if (!confirm('只导入 '+(result.name || result.slug)+' 的 SKILL.md，不执行远端脚本或依赖。确认安装？')) return;
    setRemoteSkillStatus('正在下载、校验并安装 SKILL.md…', false);
    try {
        var resp = await fetch('/api/skills/install', {
            method:'POST',
            headers:{'Content-Type':'application/json'},
            body:JSON.stringify({source:result.source, slug:result.slug, owner:result.owner || '', version:result.version || ''})
        });
        var data = await resp.json();
        if (!resp.ok || !data.ok) throw new Error(data.error || '安装失败');
        result.installed = true;
        renderRemoteSkillResults();
        setRemoteSkillStatus('已安装 '+data.name+'，并热加载到 '+data.reloaded+' 个 Agent。', false);
        loadSkillList();
    } catch (e) {
        setRemoteSkillStatus(e.message || '安装失败', true);
    }
}

async function skillEdit(name) {
    try {
        var resp = await fetch('/api/skill/detail?name='+encodeURIComponent(name));
        var data = await resp.json();
        if (!resp.ok || !data.ok) throw new Error(data.error || resp.status);
        showSkillForm(data.name, data.content);
    } catch(e) { alert(skillPanelText('skillLoadFailed', '加载失败：')+e.message); }
}

async function skillSave() {
    if (skillEditorSaving) return;
    var name = document.getElementById('skillEditName').value.trim();
    var content = document.getElementById('skillEditContent').value;
    var error = document.getElementById('skillEditorError');
    if (!name) { error.textContent = skillPanelText('skillNameRequired', '请填写技能名称'); document.getElementById('skillEditName').focus(); return; }
    skillEditorSaving = true;
    var save = document.getElementById('skillEditorSave');
    save.disabled = true;
    save.textContent = skillPanelText('skillSaving', '正在保存…');
    document.getElementById('skillEditName').disabled = true;
    document.getElementById('skillEditContent').disabled = true;
    error.textContent = '';
    var saved = false;
    try {
        var resp = await fetch('/api/skill/save', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({name:name, content:content})});
        var data = await resp.json();
        if (!resp.ok || !data.ok) throw new Error(data.error || resp.status);
        saved = true;
    } catch(e) { error.textContent = skillPanelText('skillSaveFailed', '保存失败，修改已保留：')+e.message; }
    finally {
        skillEditorSaving = false;
        save.disabled = false;
        save.textContent = skillPanelText('skillSave', '保存技能');
        document.getElementById('skillEditName').disabled = false;
        document.getElementById('skillEditContent').disabled = false;
    }
    if (saved) { closeSkillEditor(true); loadSkillList(); }
}

async function skillDelete(name) {
    if (!confirm('Delete skill '+name+'?')) return;
    try {
        var resp = await fetch('/api/skill/delete?name='+encodeURIComponent(name), {method:'POST'});
        var data = await resp.json();
        if (data.ok) loadSkillList(); else alert('Delete failed: '+(data.error||''));
    } catch(e) { alert('Delete failed: '+e.message); }
}

// skillInstallSimple: auto-detect Git URL vs local path
async function skillInstallSimple() {
    var src = document.getElementById('skillSourceInput').value.trim();
    if (!src) { alert('请输入 Git URL 或本地目录路径'); return; }
    var resultDiv = document.getElementById('skillInstallResult');
    resultDiv.innerHTML = '<div style="color:#8b5cf6;">📋 Installing...</div>';
    try {
        var isGit = src.startsWith('http://') || src.startsWith('https://') || src.startsWith('git@');
        var endpoint = isGit ? '/api/skill/install' : '/api/skill/install-local';
        var body = isGit ? {url: src} : {path: src};
        var resp = await fetch(endpoint, {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify(body)});
        var data = await resp.json();
        if (data.ok) {
            resultDiv.innerHTML = '<div style="color:#166534;background:#dcfce7;padding:8px;border-radius:4px;">✅ ' + escHtml(data.message||'Installed!') + '</div>';
            document.getElementById('skillSourceInput').value = '';
            loadSkillList();
        } else {
            resultDiv.innerHTML = '<div style="color:#dc3545;">❌ ' + escHtml(data.error||'Install failed') + '</div>';
        }
    } catch(e) {
        resultDiv.innerHTML = '<div style="color:#dc3545;">❌ '+escHtml(e.message)+'</div>';
    }
}

// skillInstallMerge: AI analysis + selective install
async function skillInstallMerge() {
    var src = document.getElementById('skillSourceInput').value.trim();
    if (!src) { alert('请输入源目录路径'); return; }
    var resultDiv = document.getElementById('skillInstallResult');
    resultDiv.innerHTML = '<div style="color:#ec4899;">🧠 AI analyzing source skills, comparing with existing... This may take 30-60 seconds.</div>';
    try {
        var resp = await fetch('/api/skill/merge', {method:'POST', headers:{'Content-Type':'application/json'}, body: JSON.stringify({path: src})});
        var data = await resp.json();
        if (data.ok) {
            resultDiv.innerHTML = '<div style="color:#166534;background:#dcfce7;padding:8px;border-radius:4px;white-space:pre-wrap;">' + escHtml(data.report || 'Merge complete') + '</div>';
            document.getElementById('skillSourceInput').value = '';
            loadSkillList();
        } else {
            resultDiv.innerHTML = '<div style="color:#dc3545;">❌ ' + escHtml(data.error||'Merge failed') + '</div>';
        }
    } catch(e) {
        resultDiv.innerHTML = '<div style="color:#dc3545;">❌ '+escHtml(e.message)+'</div>';
    }
}

async function skillReload() {
    try {
        var resp = await fetch('/api/skill/reload', {method:'POST'});
        var data = await resp.json();
        alert(data.ok ? 'Reloaded '+data.reloaded+' agent(s)' : 'Reload failed: '+(data.error||''));
    } catch(e) { alert('Reload failed: '+e.message); }
}
