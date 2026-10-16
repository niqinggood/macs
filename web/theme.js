// Theme Toggle - 亮色/暗色切换
(function() {
  const style = document.createElement('style');
  style.id = 'theme-style';
  document.head.appendChild(style);

  const darkCSS = `
    :root{--mac-modal-surface:#1a2332;--mac-modal-chrome:#141d2b;--mac-modal-border:#344257;--mac-modal-text:#e0e6f0;--mac-modal-muted:#a6b3c6;--mac-modal-accent:#8bbdad}
    .composer-media-entry,.media-generate,.media-studio-tabs button[aria-pressed="true"]{background:#253b30!important;border-color:#41624c!important;color:#c8dfce!important}
    .media-price,.media-result-status,.media-result-download,.media-history-heading button{color:#a8cbb1!important}
    body,html, .app-container{background:#0a0e1a!important;color:#e0e6f0!important}
    .sidebar{background:#111827!important;border-right:1px solid #1e2d45!important}
    .sidebar .sidebar-nav-item,.sidebar .sidebar-footer-button{color:#c0c8d4!important}
    .sidebar .sidebar-nav-item:hover,.sidebar .sidebar-footer-button:hover{background:#253246!important;color:#e0e6f0!important}
    #sidebarToolsButton[aria-expanded="true"]{background:#284236!important;color:#d7eadb!important}
    .sidebar-tools-choice{color:#e0e6f0!important}
    .sidebar-tools-choice small{color:#adb9cc!important}
    .sidebar-tools-choice:hover,.sidebar-tools-choice:focus-visible{background:#253246!important}
    .sidebar-tools-icon{color:#a6c9b0!important;background:#20382b!important;border-color:#45604d!important}
    .sidebar-header, .sidebar-header-left{background:#111827!important}
    .sidebar-header-left .logo-text{color:#e0e6f0!important}
    .sidebar-header-left .brand-latin,.sidebar-header-left .brand-divider{color:#df9ba9!important}
    .sidebar-header-left .brand-cn,.collapsed-logo small{color:#df9ba9!important}
    .collapsed-logo{color:#df9ba9!important}
    .sidebar.collapsed{background:#1a2332!important}
    .messages-area{background:#0a0e1a!important}
    .new-conversation-state,.new-conversation-state h2{color:#e5e9ed!important}
    .new-conversation-state>p{color:#9ba6af!important}
    .new-conversation-question-surface{background:#171f24!important;border-color:rgba(166,181,157,.2)!important;box-shadow:0 14px 36px rgba(0,0,0,.2)!important}
    .new-conversation-question-copy strong{color:#e5e9ed!important}
    .new-conversation-question:hover .new-conversation-question-surface,.new-conversation-question:focus-visible .new-conversation-question-surface,.new-conversation-question.is-selected .new-conversation-question-surface{background:#232c30!important;border-color:rgba(166,181,157,.46)!important}
    .new-conversation-question-arrow{color:#a6b59d!important;background:#26302a!important}
    .new-conversation-question:hover .new-conversation-question-arrow,.new-conversation-question:focus-visible .new-conversation-question-arrow,.new-conversation-question.is-selected .new-conversation-question-arrow{color:#17201a!important;background:#a6b59d!important}
    .message.assistant .message-bubble{background:#1e293b!important;color:#e0e6f0!important;box-shadow:0 1px 4px rgba(0,0,0,0.3),0 0 0 1px rgba(255,255,255,0.06)!important}
    .message.user .message-bubble{background:#DBEAFE!important;color:#1E3A5F!important}
    .chat-header{background:#111827!important;border-bottom-color:#1e2d45!important}
    .chat-header h1{color:#e0e6f0!important}
    .input-area{background:#111827!important;border-top-color:#1e2d45!important}
    .input-container{background:#1a1f2e!important;border-color:#2d3a50!important;box-shadow:none!important}
    .input-container:focus-within{border-color:#475569!important;box-shadow:none!important}
    .input-container textarea{background:transparent!important;color:#e0e6f0!important;border-color:transparent!important}
    .composer-model-menu{--picker-bg:#181f2c;--picker-text:#e2e8f0;--picker-muted:#a4adbd;--picker-border:#303a4d;--picker-hover:#232e41}
    .composer-model-trigger,.composer-agent-picker{color:#b9c2d1!important;border-color:#303a4d!important}
    .composer-model-trigger:hover,.composer-model-trigger[aria-expanded="true"]{background:#232e41!important;color:#e2e8f0!important}
    .composer-agent-picker #agentSelect{background:transparent!important;color:inherit!important}
    .composer-model-source.platform{color:#95dfb7!important;background:#19392d!important;border-color:#2c5b46!important}
    .composer-model-source.custom{color:#d0bdfa!important;background:#322743!important;border-color:#514067!important}
    .sidebar-account .topbar-auth-login,.sidebar-account .topbar-auth-center{color:#c0c8d4!important;background:transparent!important}
    .sidebar-account .topbar-auth-user,.topbar-account-menu{background:#1a2332!important;border-color:#2d3a50!important}
    .topbar-auth-name,.topbar-account-identity strong,.topbar-account-menu-item{color:#e0e6f0!important}
    #sidebarAccountSlot #topbarUserCenterButton{background:#1a2332!important;border-color:#34445a!important}
    .sidebar-account-caption,#sidebarAccountSlot .sidebar-account-stats small{color:#94a3b8!important}
    #sidebarAccountSlot .sidebar-account-stats b{color:#d4dce6!important}
    .topbar-account-menu-item:hover,.topbar-account-menu-item:focus-visible{background:#2b374b!important;color:#fff!important}
    .topbar-account-menu-logout{color:#fca5a5!important}
    .community-workspace{background:#151f2d!important;border-color:#2d3a50!important}
    .community-workspace h2,.community-post-card strong{color:#e0e6f0!important}
    .community-workspace p,.community-workspace-updated,.community-post-summary{color:#a6b3c6!important}
    .community-post-card{background:#1b293b!important;border-color:#34465f!important}
    .community-post-badge{background:#283e59!important;color:#b6d1f1!important}
    .community-post-meta{color:#94a3b8!important}
    .community-feed-loading,.community-feed-empty{color:#b4c1d2!important;border-color:#34465f!important}
    .skill-run-popover,.sidebar-more-panel,.sidebar-tools-panel,.mac-window-restore{color:#e0e6f0!important;background:#1a1f2e!important;border-color:#2d3a50!important}
    .skill-run-search-wrap{color:#b8c2d1!important;background:#232c3c!important;border-color:#2d3a50!important}
    .skill-run-item{color:#e0e6f0!important;background:#202938!important;border-color:#344155!important}
    .skill-run-item:hover,.skill-run-item:focus-visible,.skill-run-item[aria-selected="true"]{background:#263b54!important;border-color:#7397c2!important}
    .skill-run-item-description{color:#c0c8d4!important}
    .skill-run-item-meta,.skill-run-popover-subtitle,.sidebar-more-title{color:#9eabbd!important}
    .skill-run-version{color:#c0c8d4!important;background:#344155!important}
    #sidebarMorePanel .sidebar-nav-item:hover,.mac-window-restore:hover{background:#2b374b!important}
    .sidebar-all-tasks{color:#adb9cc!important}
    .sidebar-all-tasks:hover{color:#e0e6f0!important;background:#253246!important}
    .owned-skill-card{background:#1a2332!important;border-color:#34445a!important}
    .owned-skill-card:hover{border-color:#64856d!important}
    .owned-skill-card h3,.skill-editor-form label{color:#d7eadb!important}
    .owned-skill-description,.owned-skills-count,.skill-editor-hint{color:#adb9cc!important}
    .owned-skill-name{color:#94a3b8!important;background:transparent!important}
    .owned-skill-icon{color:#a6c9b0!important;background:#20382b!important;border-color:#45604d!important}
    .owned-skill-source{color:#a6c9b0!important}
    .owned-skills-categories{border-color:#34445a!important}
    .owned-skill-category-filter{background:#202b39!important;color:#aebfba!important}
    .owned-skill-category-filter:hover{background:#2a3b35!important;color:#d7eadb!important}
    .owned-skill-category-filter[aria-pressed="true"]{background:#284235!important;border-color:#597d65!important;color:#d7eadb!important}
    .owned-skill-category-count{color:#a3b5ac!important}
    .owned-skill-category-label{background:#20382b!important;color:#b0cbb8!important}
    .owned-skill-actions,.skill-editor-actions{border-color:#34445a!important}
    .owned-skill-delete{color:#dba4a4!important}
    .owned-skill-delete:hover{background:#37252c!important}
    .owned-skills-search,.skill-editor-form input,.skill-editor-form textarea{background:#111c2b!important;border-color:#34445c!important;color:#e0e6f0!important}
    .skill-editor-error{color:#fca5a5!important}
    .input-icon-btn{background:transparent!important;color:#a3a3a3!important;border-color:transparent!important}
    .input-icon-btn:hover{background:#2b3343!important;color:#f5f5f5!important}
    .send-btn{background:#e5e5e5!important;color:#171717!important;border-color:#e5e5e5!important;box-shadow:none!important}
    .send-btn:hover:not(:disabled){background:#f5f5f5!important;border-color:#f5f5f5!important}
    .send-btn:disabled{background:#343b49!important;color:#737b89!important;border-color:#343b49!important}
    .agent-name{color:#4fc3f7!important}
    .welcome-tips{background:#111827!important;border-bottom-color:#1e2d45!important}
    .mode-switch{background:#1e2d45!important}
    .mode-btn{color:#7b87a0!important}
    .mode-btn.active{background:#4fc3f7!important;color:#0a0e1a!important}
    .hb-btn{background:#1e293b!important;color:#c0c8d4!important;border-color:#2d3a50!important}
    .hb-btn:hover{background:#2d3a50!important}
    pre,code{background:rgba(0,0,0,0.3)!important}
    .log-container,.log-message{background:#111827!important;color:#7b87a0!important}
    .config-section,.config-tab{background:#111827!important;border-color:#1e2d45!important;color:#e0e6f0!important}
    #configModal .modal-content,#settingsItemEditorModal .modal-content{background:#111827!important;border-color:#2d3a50!important}
    #configModal .modal-header,#settingsItemEditorModal .modal-header{background:#1a2332!important;border-color:#2d3a50!important}
    #configModal .settings-nav,.settings-quickstart{background:#151f2e!important;border-color:#2d3a50!important;color:#e0e6f0!important}
    #configModal .settings-nav .config-tab.active{background:#284236!important;color:#d7eadb!important}
    #configModal .config-card,.settings-summary-card,.settings-capability-card,.settings-app-choice,.settings-details,.settings-editor-actions{background:#1a2332!important;border-color:#2d3a50!important}
    .settings-details-body{border-color:#2d3a50!important}
    .settings-page-intro h2,.settings-card-title,.settings-app-heading,.settings-details summary,.settings-capability-card strong,.settings-app-choice,.settings-editor-field label,#configModal .config-card-title,#configModal .config-field label{color:#e0e6f0!important}
    .settings-page-intro p,.settings-card-subtitle,.settings-capability-card p,.settings-app-choice span,.settings-editor-hint,.settings-quickstart span,.settings-connection-test,#configModal .config-hint{color:#adb9cc!important}
    .settings-health{background:#172b23!important;border-color:#315440!important;color:#b3d7bf!important}
    .settings-health.attention{background:#30271b!important;border-color:#635036!important;color:#e0c292!important}
    .settings-health > span{color:#adb9cc!important}
    .settings-chip{background:#253246!important;border-color:#34445c!important;color:#bdc9da!important}
    .settings-action-btn{background:#20382b!important;border-color:#45604d!important;color:#d7eadb!important}
    .settings-action-btn:hover{background:#2b4836!important;border-color:#64856d!important}
    .settings-action-btn.primary{background:#527b60!important;border-color:#527b60!important;color:#fff!important}
    .settings-action-btn.primary:hover{background:#446c51!important;border-color:#446c51!important}
    .settings-action-btn.danger{background:#37252c!important;border-color:#70404b!important;color:#fecaca!important}
    .settings-action-btn.danger:hover{background:#482c34!important;border-color:#975366!important}
    #configModal .slider,#settingsItemEditorModal .slider{background:#4c5c52!important}
    #configModal .slider:before,#settingsItemEditorModal .slider:before{background:#f7faf8!important}
    #configModal input:checked+.slider,#settingsItemEditorModal input:checked+.slider{background:#6c9778!important}
    #settingsItemEditorBody input,#settingsItemEditorBody textarea,#settingsItemEditorBody select{background:#111c2b!important;border-color:#34445c!important;color:#e0e6f0!important}
    .sidebar-toggle{border:1px solid #1e2d45!important;background:#1a2332!important;color:#7b87a0!important}
    .sessions-list{border-top:1px solid #1e2d45!important}
    .session-item{background:#1a2332!important;border-color:#1e2d45!important}
    .session-item:hover{background:#1e293b!important}
    .sidebar-search input{background:#1a2332!important;color:#cbd5e1!important;border-color:#1e2d45!important}
    .sidebar-search input:focus{border-color:#60a5fa!important;background:#1e293b!important}
    .session-item.active{background:linear-gradient(135deg,#4c3f7a,#2d1f5a)!important;color:#e0d8f0!important}
    .session-item.active .session-name{color:#f0e8ff!important}
    .session-item.active .session-question{color:#f7f3ff!important}
    .session-item.active .session-time{color:rgba(240,232,255,0.6)!important}
    .session-name{color:#b0b8c4!important}
    .session-question{color:#d7dce5!important}
    .session-time{color:#6b7280!important}
    .session-copy-btn{color:#4b5563!important}
    ::-webkit-scrollbar-thumb{background:#334155!important}
    ::-webkit-scrollbar-thumb:hover{background:#475569!important}
    *{scrollbar-color:#334155 transparent!important}
    .collab-result{background:#1a2332!important;color:#c8d0dc!important}
    .collab-result span{background:#1e3a5c!important;color:#b0c8e0!important}
    .agent-selector select, #agentSelect{background:#1a1f2e!important;color:#e0e6f0!important;border-color:#1e2d45!important}
    select,textarea,input[type=text],input:not([type]){border-color:#1e2d45!important;background:#1a1f2e!important;color:#e0e6f0!important}
    .message-content table{border:1px solid #2d3a50!important}
    .message-content th{background:#1e293b!important;color:#e0e6f0!important;border-color:#2d3a50!important}
    .message-content td{background:transparent!important;border-color:#2d3a50!important}
    .message-content blockquote{background:#1a2332!important;border-left:3px solid #4fc3f7!important}
    .message-content a{color:#4fc3f7!important}
    .message-content hr{border-color:#2d3a50!important}
    .message-content img{opacity:0.9!important}
    .message-content code{background:#1a2332!important;color:#e0e6f0!important}
    .team-overlay{background:var(--mac-modal-overlay)!important}
    .team-modal{background:#111827!important;box-shadow:0 20px 60px rgba(0,0,0,0.5)!important}
    .team-modal-title h2{color:#e0e6f0!important}
    .team-modal-close:hover{background:#1e293b!important;color:#c0c8d4!important}
    .team-modal-sub{color:#7b87a0!important}
    .team-card{background:#1a2332!important}
    .team-card:hover{background:#1e293b!important;border-color:#4fc3f7!important}
    .team-card-name{color:#e0e6f0!important}
    .team-card-desc{color:#7b87a0!important}
    #teamOverlay .team-card-desc{color:#a6b3c6!important}
    .team-card-icon{background:#1e293b!important}
    .team-card-icon .ico{color:#60a5fa!important}
    .team-card-roles span{background:#1e293b!important;color:#b0b8c4!important}
    .team-card-roles span:hover{background:#4fc3f7!important;color:#0a0e1a!important}
    #taskModal .task-tabs{background:#1e293b!important}
    #taskModal .task-tab{color:#7b87a0!important}
    #taskModal .task-tab.active{background:#4fc3f7!important;color:#0a0e1a!important}
    #taskContent{color:#e0e6f0!important}
    .hb-btn-settings{background:#e74c3c!important;color:#fff!important;border-color:#e74c3c!important}
    .preset-btn{background:#1a2332!important;color:#b0b8c4!important;border-color:#2d3a50!important}
    .preset-btn:hover{background:#1e293b!important;color:#e0e6f0!important;border-color:#4fc3f7!important}
    .team-mode-badge.mode-parallel{background:rgba(43,138,62,0.2)!important;color:#69db7c!important}
    .team-mode-badge.mode-sequential{background:rgba(230,119,0,0.2)!important;color:#ffa94d!important}
    .team-mode-badge.mode-hybrid{background:rgba(28,126,214,0.2)!important;color:#74c0fc!important}
    .team-flow-arrow{color:#4b5563!important}
    .pipeline-panel{background:#111827!important;color:#e0e6f0!important}
    .pipeline-header{border-bottom-color:#1e2d45!important}
    .pipeline-tabs{background:#0f172a!important}
    .pipe-tab{color:#7b87a0!important}
    .pipe-tab.active{background:#1e293b!important;color:#60a5fa!important}
    .pipe-card{background:#1a2332!important;border-color:#1e2d45!important}
    .pipe-card:hover{box-shadow:0 4px 12px rgba(0,0,0,0.4)!important}
    .pipe-card-name{color:#e0e6f0!important}
    .pipe-card-body{color:#94a3b8!important}
    .pipe-card-id{color:#64748b!important}
    .pipeline-template-card .pipe-card-footer{border-color:#344257!important}
    .pipeline-template-card .pipe-card-steps{color:#a6b3c6!important}
    .pipeline-template-open{color:#a8cbb1!important}
    #pipelineQuickRun{background:#1a2332!important;border-color:#344257!important}
    #pipelineQuickRun > span{color:#e0e6f0!important}
    .pipe-tag{background:#1e3a5f!important;color:#60a5fa!important}
    .pipe-stat-card{background:#0f172a!important}
    .pipe-stat-num{color:#60a5fa!important}
    .pipe-stat-label{color:#94a3b8!important}
    .pipe-exec-card{background:#1a2332!important;border-color:#475569!important}
    .pipe-approval-card{background:#2d1f0a!important;border-color:#78350f!important}
    .pipe-approval-output{background:#1e293b!important;color:#e0e6f0!important;border-color:#78350f!important}
    .pipe-stop-btn{background:#334155!important;color:#94a3b8!important}
    .pipe-back-btn{background:#1e3a5f!important;color:#60a5fa!important}
    .timeline-node{background:#334155!important}
    .timeline-output{background:#0f172a!important;color:#e0e6f0!important}
    .timeline-output code{background:#334155!important;color:#e0e6f0!important}
    .tool-call-item{background:#0f172a!important;border-color:#1e2d45!important}
    .tool-call-name{color:#e0e6f0!important}
    .audit-item{background:#0f172a!important}
    .audit-role{color:#e0e6f0!important}
    .pipe-empty{color:#64748b!important}
    .pipeline-close{color:#64748b!important}
    .todo-card{background:#111827!important}
    .todo-tabs{background:#0f172a!important;border-color:#1e2d45!important}
    .todo-tab{background:#1a2332!important;color:#7b87a0!important}
    .todo-tab.active{background:#1e293b!important;color:#60a5fa!important;border-color:#60a5fa!important}
    .todo-content{color:#e0e6f0!important}
    .todo-item{background:#1a2332!important}
    .todo-title{color:#e0e6f0!important}
    .todo-meta{color:#94a3b8!important}
    .todo-err{background:#3b1111!important;color:#f87171!important}
    .todo-empty{color:#64748b!important}
    .modal-content{background:#111827!important;color:#e0e6f0!important}
    .modal-header h3{color:#e0e6f0!important}
    .mac-window-header{color:#e0e6f0!important;background:#1a2332!important;border-color:#2d3a50!important}
    .mac-window-header h2,.mac-window-header h3,.mac-window-header h4{color:#e0e6f0!important}
    .log-container,.log-message{background:#0f172a!important;color:#b0b8c4!important;border-color:#1e2d45!important}
    .team-modal{background:#111827!important;box-shadow:0 20px 60px rgba(0,0,0,0.5)!important}
    .team-modal-title h2{color:#e0e6f0!important}
    .help-section{background:#111827!important;border-color:#1e2d45!important}
    .help-section-title{color:#e0e6f0!important}
    .help-hero-action-guide,.help-agentos-flow span:not([aria-hidden]){background:#20382b!important;border-color:#45604d!important;color:#d7eadb!important}
    .help-agentos-flow{color:#9bc7a6!important}
    .help-agentos-success{background:#20382b!important;border-color:#45604d!important;color:#d7eadb!important}
    .help-agentos-steps,.help-agentos-details{color:#b7c6bc!important;border-color:#45604d!important}
    .help-agentos-details summary{color:#d7eadb!important}
    .help-agentos-details pre{background:#182b21!important;border-color:#3c5443!important;color:#d7eadb!important}
    .help-text{color:#94a3b8!important}
    .help-tag{background:#1e3a5f!important;color:#60a5fa!important;border-color:#1e40af!important}
    .help-table{border-color:#1e2d45!important}
    .help-table th{background:#0f172a!important;color:#e0e6f0!important;border-color:#1e2d45!important}
    .help-table td{color:#94a3b8!important;border-color:#1e2d45!important}
    .help-table tr:nth-child(even) td{background:#0f172a!important}
    .close-modal:hover{background:#1e293b!important;color:#e0e6f0!important}
    .log-header{border-bottom-color:#1e2d45!important}
    .log-title{color:#e0e6f0!important}
    .log-status{color:#7b87a0!important}
    .log-stream{color:#94a3b8!important}
    .progress-bubble.latest{color:#e0e6f0!important}
    .log-export-btn{color:#64748b!important}
    .log-export-btn:hover{color:#60a5fa!important}
  `;

  let isDark = localStorage.getItem('kitty-dark') === '1';

  function apply() {
    style.textContent = isDark ? darkCSS : '';
    var btn = document.getElementById('themeToggle');
    if (btn) {
      var svg = isDark
        ? '<svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>'
        : '<svg viewBox="0 0 24 24" width="16" height="16" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>';
      var icon = btn.querySelector('[data-theme-icon]');
      if (icon) icon.innerHTML = svg;
      else btn.innerHTML = svg;
    }
  }

  window.toggleTheme = function() {
    isDark = !isDark;
    localStorage.setItem('kitty-dark', isDark ? '1' : '0');
    apply();
  };

  apply();
})();
