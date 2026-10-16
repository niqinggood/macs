(function(){
  var state = { deliveries: [], filter: 'all', query: '', selectedId: '', details: {}, mobileList: false, viewRequest: 0 };

  function esc(value){
    return String(value == null ? '' : value).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
  }
  function sizeText(bytes){
    var value = Number(bytes || 0);
    if (!value) return '';
    if (value < 1024) return value + ' B';
    if (value < 1048576) return (value / 1024).toFixed(1) + ' KB';
    return (value / 1048576).toFixed(1) + ' MB';
  }
  function timeText(value){
    var time = new Date(value || '');
    if (isNaN(time.getTime())) return '';
    return time.toLocaleString(window.openKittyLanguage === 'en' ? 'en-US' : 'zh-CN', {month:'short',day:'numeric',hour:'2-digit',minute:'2-digit'});
  }
  function plainText(markdown){
    return String(markdown || '').replace(/<!--[\s\S]*?-->/g,'').replace(/```[\s\S]*?```/g,' ')
      .replace(/!\[[^\]]*\]\([^)]*\)/g,' ').replace(/\[([^\]]+)\]\([^)]*\)/g,'$1')
      .replace(/^\s{0,3}#{1,6}\s+/gm,'').replace(/[*_~`>|]/g,'').replace(/^\s*[-+]\s+/gm,'').replace(/\s+/g,' ').trim();
  }
  function primaryMarkdown(delivery){
    var markdown = String((delivery && (delivery.markdown || delivery.final_output_excerpt)) || '');
    var marker = /(?:^|\n)#{2,4}\s+Final output\s*\n/i.exec(markdown);
    if (!marker) return markdown;
    var result = markdown.slice(marker.index + marker[0].length);
    var technical = /\n#{2,4}\s+(?:Artifacts|Risks|Next actions|Execution details)\s*\n/i.exec(result);
    return technical ? result.slice(0, technical.index).trim() : result.trim();
  }
  function pipelineLabel(value){
    var key = String(value || '').toLowerCase();
    var labels = {
      'investment-advisory':'投资顾问研究', 'investment-research-manager':'投研经理分析',
      'financial-intelligence-analyst':'金融情报分析', 'ai-trader':'AI 交易研判',
      'quant-trader':'量化研究', 'browser-task':'浏览器任务', 'desktop-task':'桌面任务'
    };
    if (labels[key]) return labels[key];
    if (!key) return 'macs 任务';
    return key.split(/[-_]+/).filter(Boolean).map(function(word){ return word.charAt(0).toUpperCase() + word.slice(1); }).join(' ');
  }
  function deliveryTitle(delivery){
    var firstLine = String(delivery.final_output_excerpt || '').split(/\r?\n/).map(function(line){
      return line.replace(/^\s{0,3}#{1,6}\s*/, '').trim();
    }).filter(Boolean)[0] || '';
    if (firstLine && firstLine.length <= 80) return firstLine;
    var title = String(delivery.title || '').trim();
    if (title && !/\bdelivery$/i.test(title)) return title;
    return pipelineLabel(delivery.pipeline_name);
  }
  function statusInfo(status){
    var key = String(status || '').toLowerCase();
    if (/^(completed|success|succeeded)$/.test(key)) return {key:'completed', label:'已完成'};
    if (/^(degraded|partial)$/.test(key)) return {key:'attention', label:'可用 · 待复核'};
    if (/^(running|pending|queued)$/.test(key)) return {key:'running', label:'处理中'};
    if (/^(failed|error)$/.test(key)) return {key:'failed', label:'未完成'};
    return {key:'neutral', label:status || '状态未知'};
  }
  function fileKind(artifact){
    var name = String((artifact && (artifact.name || artifact.path)) || '');
    var extension = (name.match(/\.([^.\\/]+)$/) || [])[1];
    var ext = String(extension || '').toLowerCase();
    var type = String((artifact && artifact.type) || '').toLowerCase();
    if (ext === 'pdf') return {key:'pdf', label:'PDF 文档'};
    if (/^docx?$/.test(ext)) return {key:'document', label:'Word 文档'};
    if (/^(xlsx?|csv)$/.test(ext)) return {key:'spreadsheet', label:'表格'};
    if (/^pptx?$/.test(ext)) return {key:'presentation', label:'演示文稿'};
    if (/^(png|jpe?g|webp|gif|svg)$/.test(ext) || type === 'image') return {key:'image', label:'图片'};
    if (/^html?$/.test(ext) || type === 'web') return {key:'web', label:'网页'};
    if (/^(md|txt)$/.test(ext) || type === 'report') return {key:'report', label:'报告'};
    if (/^(json|ya?ml)$/.test(ext) || type === 'data') return {key:'data', label:'数据附件'};
    return {key:'file', label:'文件'};
  }
  function friendlyFileName(artifact){
    var original = String((artifact && (artifact.name || artifact.path)) || '文件');
    var lower = original.toLowerCase();
    if (lower.indexOf('structured_result') >= 0) return '结构化结果数据';
    if (lower.indexOf('full_states_log') >= 0) return '分析过程数据';
    if (lower.indexOf('screenshot') >= 0) return '任务截图';
    return original.split(/[\\/]/).pop().replace(/\.[^.]+$/,'').replace(/[_-]+/g,' ').trim() || fileKind(artifact).label;
  }
  function hasContent(delivery){
    return Boolean(delivery && (plainText(delivery.final_output_excerpt) || (Array.isArray(delivery.artifacts) && delivery.artifacts.length)));
  }
  function filteredDeliveries(){
    var query = state.query.toLowerCase();
    return state.deliveries.filter(function(delivery){
      if (!hasContent(delivery)) return false;
      var status = statusInfo(delivery.status);
      if (state.filter === 'completed' && status.key !== 'completed') return false;
      if (state.filter === 'attention' && status.key !== 'attention') return false;
      if (!query) return true;
      var haystack = [deliveryTitle(delivery), delivery.pipeline_name, plainText(delivery.final_output_excerpt)]
        .concat((delivery.artifacts || []).map(function(item){ return item.name || item.path || ''; })).join(' ').toLowerCase();
      return haystack.indexOf(query) >= 0;
    });
  }
  function fileIcon(){
    return '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 3h7l4 4v14H7z"/><path d="M14 3v5h5"/><path d="M10 13h5M10 17h5"/></svg>';
  }

  function ensureStyles(){
    if (document.getElementById('artifactCenterStyle')) return;
    var style = document.createElement('style');
    style.id = 'artifactCenterStyle';
    style.textContent = [
      '#artifactCenterOverlay{position:fixed;inset:0;z-index:9998;display:none;align-items:center;justify-content:center;padding:28px;background:rgba(232,237,242,.68);backdrop-filter:blur(18px);-webkit-backdrop-filter:blur(18px)}',
      '#artifactCenterOverlay .artifact-panel{width:min(1180px,96vw);height:min(820px,90vh);overflow:hidden;display:flex;flex-direction:column;background:#fbfcfd;border:1px solid rgba(112,128,144,.2);border-radius:20px;box-shadow:0 28px 90px rgba(58,72,86,.18),0 2px 8px rgba(58,72,86,.08)}',
      '#artifactCenterOverlay .artifact-header{min-height:76px;padding:18px 24px;display:flex;align-items:center;justify-content:space-between;background:rgba(255,255,255,.92);border-bottom:1px solid #e5e9ee}',
      '#artifactCenterOverlay .artifact-title{display:flex;align-items:center;gap:13px}#artifactCenterOverlay .artifact-title-icon{width:40px;height:40px;display:flex;align-items:center;justify-content:center;color:#1769aa;background:#eaf3fb;border-radius:12px}',
      '#artifactCenterOverlay .artifact-title-icon svg{width:22px;height:22px;fill:none;stroke:currentColor;stroke-width:1.7;stroke-linecap:round;stroke-linejoin:round}#artifactCenterOverlay .artifact-title h2{margin:0;color:#243447;font-size:calc(22px * var(--macs-font-scale, 1));letter-spacing:-.02em}#artifactCenterOverlay .artifact-title p{margin:3px 0 0;color:#7a8796;font-size:calc(13.2px * var(--macs-font-scale, 1))}',
      '#artifactCenterOverlay .artifact-close{width:34px;height:34px;display:flex;align-items:center;justify-content:center;color:#627184;background:#f3f5f7;border:0;border-radius:50%;cursor:pointer;font-size:calc(24.2px * var(--macs-font-scale, 1));line-height:1}#artifactCenterOverlay .artifact-close:hover{color:#26384c;background:#e9edf1}',
      '#artifactCenterOverlay .artifact-toolbar{padding:14px 24px 12px;display:flex;align-items:center;gap:10px;background:#fff}#artifactCenterOverlay .artifact-search{height:38px;min-width:240px;flex:1;display:flex;align-items:center;gap:8px;padding:0 12px;color:#7b8794;background:#f5f7f9;border:1px solid #e4e8ed;border-radius:10px}',
      '#artifactCenterOverlay .artifact-search svg{width:16px;height:16px;fill:none;stroke:currentColor;stroke-width:2}#artifactCenterOverlay .artifact-search input{width:100%;color:#2d3f52;background:transparent;border:0;outline:0;font:inherit;font-size:calc(14.3px * var(--macs-font-scale, 1))}',
      '#artifactCenterOverlay .artifact-filters{padding:3px;display:flex;gap:2px;background:#f0f3f6;border-radius:10px}#artifactCenterOverlay .artifact-filter{height:32px;padding:0 12px;color:#687688;background:transparent;border:0;border-radius:8px;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-filter.active{color:#1769aa;background:#fff;box-shadow:0 1px 4px rgba(47,64,82,.1)}',
      '#artifactCenterOverlay .artifact-stats{padding:0 24px 14px;display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:9px;background:#fff}#artifactCenterOverlay .artifact-stat{min-height:58px;padding:10px 13px;display:flex;align-items:center;gap:10px;background:#f8fafb;border:1px solid #e6eaee;border-radius:11px}#artifactCenterOverlay .artifact-stat b{min-width:30px;color:#25384c;font-size:calc(22px * var(--macs-font-scale, 1));font-weight:680}#artifactCenterOverlay .artifact-stat span{color:#7c8997;font-size:calc(12.1px * var(--macs-font-scale, 1));line-height:1.35}',
      '#artifactCenterOverlay .artifact-workspace{min-height:0;flex:1;display:grid;grid-template-columns:minmax(280px,.8fr) minmax(0,1.6fr);background:#f5f7f9;border-top:1px solid #e8ebef}#artifactCenterOverlay .artifact-list-pane{min-width:0;overflow:auto;padding:16px 14px 20px 18px}#artifactCenterOverlay .artifact-list{display:grid;gap:10px}',
      '#artifactCenterOverlay .artifact-delivery-card{padding:14px;display:grid;grid-template-columns:minmax(0,1fr);gap:9px;background:#fff;border:1px solid #e2e7ec;border-radius:11px;box-shadow:0 1px 2px rgba(37,55,73,.025);cursor:pointer;transition:border-color .16s ease,box-shadow .16s ease}#artifactCenterOverlay .artifact-delivery-card:hover,#artifactCenterOverlay .artifact-delivery-card.selected{border-color:#8fae9a;box-shadow:0 3px 12px rgba(40,74,53,.06)}#artifactCenterOverlay .artifact-delivery-card:focus-visible{outline:2px solid #91ad9b;outline-offset:2px}',
      '#artifactCenterOverlay .artifact-card-top{display:flex;align-items:center;gap:9px;min-width:0}#artifactCenterOverlay .artifact-card-top h3{min-width:0;margin:0;overflow:hidden;color:#26384a;font-size:calc(15.4px * var(--macs-font-scale, 1));font-weight:680;text-overflow:ellipsis;white-space:nowrap}#artifactCenterOverlay .artifact-status{flex:0 0 auto;padding:4px 8px;color:#267357;background:#e9f6ef;border-radius:999px;font-size:calc(11px * var(--macs-font-scale, 1));font-weight:650}#artifactCenterOverlay .artifact-status.attention{color:#93651c;background:#fff3d9}#artifactCenterOverlay .artifact-status.running{color:#1769aa;background:#eaf3fb}#artifactCenterOverlay .artifact-status.failed{color:#a7444d;background:#fbeaec}',
      '#artifactCenterOverlay .artifact-excerpt{margin:8px 0 9px;overflow:hidden;display:-webkit-box;color:#647385;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6;-webkit-box-orient:vertical;-webkit-line-clamp:2}#artifactCenterOverlay .artifact-meta{display:flex;align-items:center;gap:7px;flex-wrap:wrap;color:#8a96a3;font-size:calc(12.1px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-meta span+span:before{content:"·";margin-right:7px;color:#c1c7ce}',
      '#artifactCenterOverlay .artifact-file-summary{margin-top:10px;display:flex;align-items:center;gap:7px;flex-wrap:wrap}#artifactCenterOverlay .artifact-file-chip{display:inline-flex;align-items:center;gap:5px;padding:4px 8px;color:#53697e;background:#f2f6f8;border-radius:7px;font-size:calc(11px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-file-chip svg{width:13px;height:13px;fill:none;stroke:currentColor;stroke-width:1.8}#artifactCenterOverlay .artifact-open{align-self:center;color:#1769aa;background:transparent;border:0;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1));font-weight:650;white-space:nowrap}',
      '#artifactCenterOverlay .artifact-detail-pane{min-width:0;overflow:auto;background:#fff;border-left:1px solid #e0e5ea}#artifactCenterOverlay .artifact-detail{padding:22px 24px 30px}#artifactCenterOverlay .artifact-pane-label{margin:0 0 13px;color:#7b8581;font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:600;letter-spacing:.04em}#artifactCenterOverlay .artifact-detail-head{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:16px}#artifactCenterOverlay .artifact-detail-head h3{margin:0;color:#22364a;font-size:calc(19.8px * var(--macs-font-scale, 1));line-height:1.35;overflow-wrap:anywhere}#artifactCenterOverlay .artifact-detail-back{display:none;color:#1769aa;background:transparent;border:0;cursor:pointer;font-size:calc(13.2px * var(--macs-font-scale, 1))}',
      '#artifactCenterOverlay .artifact-readable{color:#35485b;font-size:calc(14.3px * var(--macs-font-scale, 1));line-height:1.72}#artifactCenterOverlay .artifact-readable h2,#artifactCenterOverlay .artifact-readable h3,#artifactCenterOverlay .artifact-readable h4{margin:20px 0 8px;color:#263b50;line-height:1.35}#artifactCenterOverlay .artifact-readable h2{font-size:calc(18.7px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-readable h3{font-size:calc(16.5px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-readable h4{font-size:calc(15.4px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-readable p{margin:7px 0}#artifactCenterOverlay .artifact-readable .line-item{position:relative;padding-left:15px}#artifactCenterOverlay .artifact-readable .line-item:before{content:"";position:absolute;left:2px;top:.72em;width:4px;height:4px;background:#6e9bc2;border-radius:50%}#artifactCenterOverlay .artifact-readable .quote{margin:10px 0;padding:9px 11px;color:#5f7082;background:#f2f6f9;border-left:3px solid #9cbdd8;border-radius:0 7px 7px 0}#artifactCenterOverlay .artifact-table-row{display:grid;grid-template-columns:repeat(var(--artifact-cols),minmax(0,1fr));border:1px solid #e4e9ee;border-bottom:0;background:#fbfcfd;font-size:calc(12.1px * var(--macs-font-scale, 1));line-height:1.45}#artifactCenterOverlay .artifact-table-row:last-of-type{border-bottom:1px solid #e4e9ee}#artifactCenterOverlay .artifact-table-row.header{color:#40566b;background:#f0f5f8;font-weight:680}#artifactCenterOverlay .artifact-table-row span{min-width:0;padding:7px 8px;overflow-wrap:anywhere}#artifactCenterOverlay .artifact-table-row span+span{border-left:1px solid #e4e9ee}',
      '#artifactCenterOverlay .artifact-attachments{margin-top:22px;padding-top:17px;border-top:1px solid #e7ebef}#artifactCenterOverlay .artifact-attachments h4{margin:0 0 10px;color:#516274;font-size:calc(13.2px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-attachment{padding:10px 0;display:flex;align-items:center;gap:10px;border-top:1px solid #eff2f4}#artifactCenterOverlay .artifact-attachment:first-of-type{border-top:0}#artifactCenterOverlay .artifact-file-icon{width:34px;height:34px;flex:0 0 auto;display:flex;align-items:center;justify-content:center;color:#2e709f;background:#edf5fa;border-radius:9px}#artifactCenterOverlay .artifact-file-icon svg{width:18px;height:18px;fill:none;stroke:currentColor;stroke-width:1.7}',
      '#artifactCenterOverlay .artifact-file-info{min-width:0;flex:1}#artifactCenterOverlay .artifact-file-info strong,#artifactCenterOverlay .artifact-file-info small{overflow:hidden;display:block;text-overflow:ellipsis;white-space:nowrap}#artifactCenterOverlay .artifact-file-info strong{color:#3b4d60;font-size:calc(13.2px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-file-info small{margin-top:2px;color:#95a0ab;font-size:calc(11px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-link{height:30px;padding:0 10px;display:inline-flex;align-items:center;justify-content:center;color:#1769aa;background:#fff;border:1px solid #cddce9;border-radius:8px;text-decoration:none;font-size:calc(12.1px * var(--macs-font-scale, 1));font-weight:620}#artifactCenterOverlay .artifact-link:hover{background:#f1f7fb}',
      '#artifactCenterOverlay .artifact-technical{margin-top:16px;color:#82909e;font-size:calc(12.1px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-technical summary{cursor:pointer}#artifactCenterOverlay .artifact-technical dl{display:grid;grid-template-columns:auto 1fr;gap:6px 10px;margin:10px 0 0}#artifactCenterOverlay .artifact-technical dt{color:#9ca6b0}#artifactCenterOverlay .artifact-technical dd{margin:0;overflow-wrap:anywhere;color:#667586}',
      '#artifactCenterOverlay .artifact-loading,#artifactCenterOverlay .artifact-empty{min-height:240px;padding:38px;display:flex;align-items:center;justify-content:center;flex-direction:column;text-align:center;color:#7f8c9a;background:#fff;border:1px dashed #d6dce2;border-radius:13px}#artifactCenterOverlay .artifact-empty strong{color:#405367;font-size:calc(15.4px * var(--macs-font-scale, 1))}#artifactCenterOverlay .artifact-empty span{max-width:420px;margin-top:7px;font-size:calc(13.2px * var(--macs-font-scale, 1));line-height:1.6}#artifactCenterOverlay .artifact-loading-dots{display:flex;gap:6px}#artifactCenterOverlay .artifact-loading-dots i{width:7px;height:7px;background:#7aa5c7;border-radius:50%;animation:artifactPulse 1s ease-in-out infinite}#artifactCenterOverlay .artifact-loading-dots i:nth-child(2){animation-delay:.14s}#artifactCenterOverlay .artifact-loading-dots i:nth-child(3){animation-delay:.28s}',
      '@keyframes artifactPulse{0%,70%,100%{opacity:.3;transform:translateY(0)}35%{opacity:1;transform:translateY(-4px)}}#artifactCenterOverlay .artifact-footer{min-height:42px;padding:0 24px;display:flex;align-items:center;justify-content:space-between;color:#8d98a4;background:#fff;border-top:1px solid #e5e9ee;font-size:calc(11px * var(--macs-font-scale, 1))}',
      '#artifactCenterOverlay .artifact-panel{--artifact-surface:var(--mac-modal-surface,#fff);--artifact-chrome:var(--mac-modal-chrome,#f5f8f6);--artifact-border:var(--mac-modal-border,#e0e7e2);--artifact-text:var(--mac-modal-text,#293e33);--artifact-muted:var(--mac-modal-muted,#7b8581);--artifact-accent:var(--mac-modal-accent,#426953);background:var(--artifact-surface);border-color:var(--artifact-border)}',
      '#artifactCenterOverlay .artifact-header,#artifactCenterOverlay .artifact-toolbar,#artifactCenterOverlay .artifact-stats,#artifactCenterOverlay .artifact-detail-pane,#artifactCenterOverlay .artifact-footer,#artifactCenterOverlay .artifact-delivery-card,#artifactCenterOverlay .artifact-empty,#artifactCenterOverlay .artifact-loading{background:var(--artifact-surface);border-color:var(--artifact-border)}',
      '#artifactCenterOverlay .artifact-workspace,#artifactCenterOverlay .artifact-search,#artifactCenterOverlay .artifact-filters,#artifactCenterOverlay .artifact-stat,#artifactCenterOverlay .artifact-title-icon,#artifactCenterOverlay .artifact-close,#artifactCenterOverlay .artifact-file-chip,#artifactCenterOverlay .artifact-file-icon,#artifactCenterOverlay .artifact-readable .quote{background:var(--artifact-chrome);border-color:var(--artifact-border)}',
      '#artifactCenterOverlay .artifact-title h2,#artifactCenterOverlay .artifact-card-top h3,#artifactCenterOverlay .artifact-detail-head h3,#artifactCenterOverlay .artifact-stat b,#artifactCenterOverlay .artifact-readable,#artifactCenterOverlay .artifact-readable h2,#artifactCenterOverlay .artifact-readable h3,#artifactCenterOverlay .artifact-readable h4,#artifactCenterOverlay .artifact-file-info strong,#artifactCenterOverlay .artifact-empty strong,#artifactCenterOverlay .artifact-search input{color:var(--artifact-text)}',
      '#artifactCenterOverlay .artifact-excerpt,#artifactCenterOverlay .artifact-meta,#artifactCenterOverlay .artifact-title p,#artifactCenterOverlay .artifact-stat span,#artifactCenterOverlay .artifact-footer,#artifactCenterOverlay .artifact-empty,#artifactCenterOverlay .artifact-pane-label,#artifactCenterOverlay .artifact-file-info small{color:var(--artifact-muted)}',
      '#artifactCenterOverlay .artifact-link,#artifactCenterOverlay .artifact-filter.active{background:var(--artifact-chrome);color:var(--artifact-accent);border-color:var(--artifact-border)}#artifactCenterOverlay .artifact-open,#artifactCenterOverlay .artifact-title-icon,#artifactCenterOverlay .artifact-detail-back{color:var(--artifact-accent)}#artifactCenterOverlay .artifact-delivery-card.selected{background:var(--artifact-chrome);border-color:var(--artifact-accent)}',
      '#artifactCenterOverlay .artifact-table-row,#artifactCenterOverlay .artifact-table-row.header{background:var(--artifact-chrome);border-color:var(--artifact-border);color:var(--artifact-text)}#artifactCenterOverlay .artifact-table-row span+span,#artifactCenterOverlay .artifact-attachments,#artifactCenterOverlay .artifact-attachment{border-color:var(--artifact-border)}#artifactCenterOverlay .artifact-filter:not(.active),#artifactCenterOverlay .artifact-close,#artifactCenterOverlay .artifact-file-chip,#artifactCenterOverlay .artifact-technical,#artifactCenterOverlay .artifact-technical dt,#artifactCenterOverlay .artifact-technical dd,#artifactCenterOverlay .artifact-attachments h4{color:var(--artifact-muted)}',
      '@media(max-width:820px){#artifactCenterOverlay{padding:0;align-items:stretch}#artifactCenterOverlay .artifact-panel{width:100vw;height:100dvh;max-height:none;border:0;border-radius:0}#artifactCenterOverlay .artifact-header{padding:15px 16px}#artifactCenterOverlay .artifact-title p{display:none}#artifactCenterOverlay .artifact-toolbar{padding:10px 12px;flex-wrap:wrap}#artifactCenterOverlay .artifact-search{width:100%;flex-basis:100%}#artifactCenterOverlay .artifact-filters{width:100%;display:grid;grid-template-columns:repeat(3,1fr)}#artifactCenterOverlay .artifact-stats{padding:0 12px 10px;grid-template-columns:repeat(2,1fr)}#artifactCenterOverlay .artifact-workspace{grid-template-columns:minmax(0,1fr)}#artifactCenterOverlay .artifact-list-pane{padding:12px}#artifactCenterOverlay .artifact-delivery-card{grid-template-columns:1fr}#artifactCenterOverlay .artifact-open{justify-self:start}#artifactCenterOverlay .artifact-workspace.has-detail:not(.show-list) .artifact-list-pane{display:none}#artifactCenterOverlay .artifact-workspace.show-list .artifact-detail-pane,#artifactCenterOverlay .artifact-workspace:not(.has-detail) .artifact-detail-pane{display:none}#artifactCenterOverlay .artifact-detail-pane{border-left:0}#artifactCenterOverlay .artifact-detail{padding:18px 16px 28px}#artifactCenterOverlay .artifact-detail-back{display:inline-block}#artifactCenterOverlay .artifact-footer{padding:0 14px}}',
      '@media(prefers-reduced-motion:reduce){#artifactCenterOverlay .artifact-loading-dots i{animation:none}#artifactCenterOverlay .artifact-delivery-card{transition:none}}'
    ].join('\n');
    document.head.appendChild(style);
  }

  function ensurePanel(){
    ensureStyles();
    var panel = document.getElementById('artifactCenterOverlay');
    if (panel) return panel;
    panel = document.createElement('div');
    panel.id = 'artifactCenterOverlay';
    panel.className = 'artifact-overlay';
    panel.setAttribute('role','dialog'); panel.setAttribute('aria-modal','true'); panel.setAttribute('aria-labelledby','artifactCenterTitle');
    panel.innerHTML = '<section class="artifact-panel">' +
      '<header class="artifact-header"><div class="artifact-title"><span class="artifact-title-icon"><svg viewBox="0 0 24 24"><path d="M4 7.5h16v12H4z"/><path d="M2.5 4.5h19v3h-19z"/><path d="M9 11h6"/></svg></span><div><h2 id="artifactCenterTitle" data-i18n="artifactCenter">交付物</h2><p data-i18n="artifactCenterSubtitle">左侧选择交付物，右侧预览结果并下载附件</p></div></div><button class="artifact-close" type="button" data-action="close" aria-label="关闭">&times;</button></header>' +
      '<div class="artifact-toolbar"><label class="artifact-search"><svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="m20 20-4-4"/></svg><input id="artifactCenterSearch" type="search" placeholder="搜索文件或任务" data-i18n-placeholder="artifactCenterSearch" autocomplete="off"></label><div class="artifact-filters" role="tablist"><button class="artifact-filter active" type="button" data-filter="all">全部</button><button class="artifact-filter" type="button" data-filter="completed">已完成</button><button class="artifact-filter" type="button" data-filter="attention">待复核</button></div></div>' +
      '<div class="artifact-stats" id="artifactCenterStats"></div><div class="artifact-workspace" id="artifactCenterWorkspace"><div class="artifact-list-pane"><p class="artifact-pane-label">交付物列表</p><div class="artifact-list" id="artifactCenterList"></div></div><aside class="artifact-detail-pane" id="artifactCenterDetail" aria-label="交付物预览"></aside></div>' +
      '<footer class="artifact-footer"><span data-i18n="artifactCenterHint">报告、文档、表格等交付物会显示在这里</span><span id="artifactCenterUpdated"></span></footer></section>';
    panel.addEventListener('click', handleClick);
    panel.querySelector('#artifactCenterSearch').addEventListener('input', function(event){ state.query = String(event.target.value || '').trim(); state.mobileList = false; renderList(); });
    panel.addEventListener('keydown', function(event){
      if (event.key === 'Escape') closeArtifactCenter();
      if ((event.key === 'Enter' || event.key === ' ') && event.target.classList.contains('artifact-delivery-card')) { event.preventDefault(); selectDelivery(event.target.getAttribute('data-id')); }
    });
    panel.querySelectorAll("[data-i18n]").forEach(function(node) { if (window.openKittyT) node.textContent = window.openKittyT(node.dataset.i18n, node.textContent); });
    var search = panel.querySelector("#artifactCenterSearch");
    if (window.openKittyT) search.placeholder = window.openKittyT("artifactCenterSearch", search.placeholder);
    document.body.appendChild(panel);
    return panel;
  }

  function handleClick(event){
    var panel = document.getElementById('artifactCenterOverlay');
    if (event.target === panel || event.target.closest('[data-action="close"]')) { closeArtifactCenter(); return; }
    var filter = event.target.closest('[data-filter]');
    if (filter) {
      state.filter = filter.getAttribute('data-filter') || 'all';
      state.mobileList = false;
      panel.querySelectorAll('[data-filter]').forEach(function(button){ button.classList.toggle('active', button === filter); });
      renderList(); return;
    }
    if (event.target.closest('a')) return;
    if (event.target.closest('[data-action="back"]')) {
      state.mobileList = true; renderList();
      var selectedCard = panel.querySelector('.artifact-delivery-card.selected');
      if (selectedCard) selectedCard.focus({preventScroll:true});
      return;
    }
    var card = event.target.closest('.artifact-delivery-card');
    if (card) selectDelivery(card.getAttribute('data-id'));
  }
  function renderStats(){
    var target = document.getElementById('artifactCenterStats');
    if (!target) return;
    var items = state.deliveries.filter(hasContent);
    var values = [
      [items.length,'交付物'],
      [items.reduce(function(n,item){return n + ((item.artifacts || []).length);},0),'附件文件'],
      [items.filter(function(item){return statusInfo(item.status).key === 'completed';}).length,'已完成'],
      [items.filter(function(item){return statusInfo(item.status).key === 'attention';}).length,'待复核']
    ];
    target.innerHTML = values.map(function(item){return '<div class="artifact-stat"><b>'+esc(item[0])+'</b><span>'+esc(item[1])+'</span></div>';}).join('');
  }
  function renderFileChips(artifacts){
    if (!Array.isArray(artifacts) || !artifacts.length) return '';
    var html = artifacts.slice(0,2).map(function(artifact){
      return '<span class="artifact-file-chip">'+fileIcon()+esc(fileKind(artifact).label)+(artifact.size?' · '+esc(sizeText(artifact.size)):'')+'</span>';
    }).join('');
    if (artifacts.length > 2) html += '<span class="artifact-file-chip">+'+(artifacts.length-2)+' 个附件</span>';
    return '<div class="artifact-file-summary">'+html+'</div>';
  }
  function renderCard(delivery){
    var status = statusInfo(delivery.status);
    var excerpt = plainText(delivery.final_output_excerpt);
    var meta = [pipelineLabel(delivery.pipeline_name), timeText(delivery.created_at)];
    if (delivery.evidence_level && delivery.evidence_level !== 'none') meta.push('来源 '+Number(delivery.source_count || 0)+' 条');
    var selected = state.selectedId === delivery.exec_id;
    return '<article class="artifact-delivery-card'+(selected?' selected':'')+'" data-id="'+esc(delivery.exec_id)+'" role="button" tabindex="0" aria-pressed="'+selected+'"><div><div class="artifact-card-top"><h3>'+esc(deliveryTitle(delivery))+'</h3><span class="artifact-status '+esc(status.key)+'">'+esc(status.label)+'</span></div>'+
      '<p class="artifact-excerpt">'+esc(excerpt || '此交付物包含附件。')+'</p><div class="artifact-meta">'+meta.filter(Boolean).map(function(item){return '<span>'+esc(item)+'</span>';}).join('')+'</div>'+renderFileChips(delivery.artifacts || [])+'</div><span class="artifact-open" aria-hidden="true">'+(selected?'正在预览':'查看交付物 →')+'</span></article>';
  }
  function renderList(){
    renderStats();
    var list = document.getElementById('artifactCenterList');
    var workspace = document.getElementById('artifactCenterWorkspace');
    if (!list || !workspace) return;
    var items = filteredDeliveries();
    if (!items.some(function(item){ return item.exec_id === state.selectedId; })) state.selectedId = items.length ? items[0].exec_id : '';
    var activeCard = document.activeElement && document.activeElement.closest('.artifact-delivery-card');
    var focusId = activeCard ? activeCard.getAttribute('data-id') : '';
    var emptyHint = state.query || state.filter !== 'all' ? '可以换一个关键词，或切换上方状态筛选。' : '完成任务后，报告、文档和其他交付物会显示在这里。';
    list.innerHTML = items.length ? items.map(renderCard).join('') : '<div class="artifact-empty"><strong>暂无交付物</strong><span>'+esc(emptyHint)+'</span></div>';
    if (focusId) {
      var focused = Array.from(list.querySelectorAll('.artifact-delivery-card')).find(function(card){ return card.getAttribute('data-id') === focusId; });
      if (focused) focused.focus({preventScroll:true});
    }
    workspace.classList.toggle('has-detail', Boolean(state.selectedId));
    workspace.classList.toggle('show-list', state.mobileList);
    var detail = document.getElementById('artifactCenterDetail');
    if (!state.selectedId) {
      detail.removeAttribute('data-delivery-id');
      detail.innerHTML = '<div class="artifact-detail"><p class="artifact-pane-label">交付物预览</p><div class="artifact-empty"><strong>暂无可预览的交付物</strong><span>'+esc(emptyHint)+'</span></div></div>';
    } else if (detail.getAttribute('data-delivery-id') !== state.selectedId) {
      loadSelectedDelivery(state.selectedId);
    }
  }
  function renderReadable(markdown){
    var output = [];
    var lines = String(markdown || '').replace(/<!--[\s\S]*?-->/g,'').split(/\r?\n/);
    lines.forEach(function(raw, index){
      var line = raw.trim();
      if (!line) return;
      var heading = line.match(/^(#{1,4})\s+(.+)$/);
      if (heading) { var level=Math.min(4,heading[1].length+1); output.push('<h'+level+'>'+esc(heading[2].replace(/[*_`]/g,''))+'</h'+level+'>'); return; }
      if (/^>\s?/.test(line)) { output.push('<div class="quote">'+esc(line.replace(/^>\s?/,'').replace(/[*_`]/g,''))+'</div>'); return; }
      if (/^(?:[-+*]|\d+[.)])\s+/.test(line)) { output.push('<p class="line-item">'+esc(line.replace(/^(?:[-+*]|\d+[.)])\s+/, '').replace(/[*_`]/g,''))+'</p>'); return; }
      if (/^\|.*\|$/.test(line)) {
        var cells = line.slice(1,-1).split('|').map(function(cell){ return cell.trim().replace(/[*_`]/g,''); });
        if (cells.every(function(cell){ return /^:?-{3,}:?$/.test(cell); })) return;
        var next = String(lines[index + 1] || '').trim();
        var header = /^\|(?:\s*:?-{3,}:?\s*\|)+$/.test(next);
        output.push('<div class="artifact-table-row'+(header?' header':'')+'" style="--artifact-cols:'+cells.length+'">'+cells.map(function(cell){return '<span>'+esc(cell)+'</span>';}).join('')+'</div>');
        return;
      }
      output.push('<p>'+esc(line.replace(/\|/g,' · ').replace(/[*_`]/g,''))+'</p>');
    });
    return output.join('') || '<p>暂无可展示的文字结论。</p>';
  }
  function renderAttachments(artifacts){
    if (!Array.isArray(artifacts) || !artifacts.length) return '';
    return '<section class="artifact-attachments"><h4>相关附件</h4>'+artifacts.map(function(artifact){
      var kind=fileKind(artifact), original=String(artifact.name || artifact.path || '');
      var action=artifact.download_url?'<a class="artifact-link" href="'+esc(artifact.download_url)+'" target="_blank" rel="noopener">下载</a>':'';
      return '<div class="artifact-attachment"><span class="artifact-file-icon">'+fileIcon()+'</span><div class="artifact-file-info"><strong>'+esc(friendlyFileName(artifact))+'</strong><small>'+esc(kind.label)+(artifact.size?' · '+esc(sizeText(artifact.size)):'')+(original?' · '+esc(original):'')+'</small></div>'+action+'</div>';
    }).join('')+'</section>';
  }
  function renderDetail(delivery){
    var target=document.getElementById('artifactCenterDetail');
    if (!target || !delivery) return;
    var status=statusInfo(delivery.status), detailURL=delivery.exec_id?'/web/execution.html?id='+encodeURIComponent(delivery.exec_id):'';
    target.innerHTML='<div class="artifact-detail"><p class="artifact-pane-label">交付物预览</p><div class="artifact-detail-head"><div><button class="artifact-detail-back" type="button" data-action="back">← 返回交付物列表</button><h3>'+esc(deliveryTitle(delivery))+'</h3><div class="artifact-meta"><span>'+esc(pipelineLabel(delivery.pipeline_name))+'</span><span class="artifact-status '+esc(status.key)+'">'+esc(status.label)+'</span><span>'+esc(timeText(delivery.created_at))+'</span></div></div>'+(detailURL?'<a class="artifact-link" href="'+esc(detailURL)+'" target="_blank" rel="noopener">运行详情</a>':'')+'</div><div class="artifact-readable">'+renderReadable(primaryMarkdown(delivery))+'</div>'+renderAttachments(delivery.artifacts || [])+'<details class="artifact-technical"><summary>技术信息</summary><dl><dt>任务编号</dt><dd>'+esc(delivery.exec_id || '-')+'</dd><dt>执行流程</dt><dd>'+esc(delivery.pipeline_name || '-')+'</dd><dt>证据等级</dt><dd>'+esc(delivery.evidence_level || '-')+'</dd><dt>风险等级</dt><dd>'+esc(delivery.risk_level || '-')+'</dd></dl></details></div>';
  }
  function selectDelivery(id){
    if (!id || !filteredDeliveries().some(function(item){ return item.exec_id === id; })) return;
    state.selectedId=id; state.mobileList=false; renderList();
  }
  function loadSelectedDelivery(id){
    var target=document.getElementById('artifactCenterDetail');
    target.setAttribute('data-delivery-id', id);
    target.scrollTop=0;
    if (state.details[id]) { renderDetail(state.details[id]); return; }
    var fallback=state.deliveries.find(function(item){return item.exec_id===id;});
    var request = state.viewRequest;
    target.innerHTML='<div class="artifact-loading"><div class="artifact-loading-dots"><i></i><i></i><i></i></div></div>';
    fetch('/api/delivery/summary?exec_id='+encodeURIComponent(id)).then(function(response){if(!response.ok)throw new Error('HTTP '+response.status);return response.json();}).then(function(data){
      if (request !== state.viewRequest) return;
      var delivery=data.delivery || fallback; state.details[id]=delivery; if(state.selectedId===id)renderDetail(delivery);
    }).catch(function(){
      if (request !== state.viewRequest || state.selectedId !== id) return;
      if(fallback)renderDetail(fallback);else target.innerHTML='<div class="artifact-empty"><strong>交付物暂时无法加载</strong><span>请稍后重试。</span></div>';
    });
  }
  function loadDeliveries(){
    var list=document.getElementById('artifactCenterList'), stats=document.getElementById('artifactCenterStats');
    var detail=document.getElementById('artifactCenterDetail'), workspace=document.getElementById('artifactCenterWorkspace');
    var request=state.viewRequest;
    state.deliveries=[];
    workspace.classList.remove('has-detail','show-list');
    detail.removeAttribute('data-delivery-id');
    detail.innerHTML='<div class="artifact-loading"><div class="artifact-loading-dots"><i></i><i></i><i></i></div></div>';
    document.getElementById('artifactCenterUpdated').textContent='';
    if(stats)stats.innerHTML='';
    if(list)list.innerHTML='<div class="artifact-loading"><div class="artifact-loading-dots"><i></i><i></i><i></i></div></div>';
    fetch('/api/deliveries?limit=100').then(function(response){if(!response.ok)throw new Error('HTTP '+response.status);return response.json();}).then(function(data){
      if (request !== state.viewRequest) return;
      state.deliveries=Array.isArray(data.deliveries)?data.deliveries:[];
      var updated=document.getElementById('artifactCenterUpdated');
      if(updated)updated.textContent='更新于 '+new Date().toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'});
      renderList();
    }).catch(function(error){
      if (request !== state.viewRequest) return;
      if(list)list.innerHTML='<div class="artifact-empty"><strong>交付物加载失败</strong><span>'+esc(error.message || error)+'</span></div>';
      detail.innerHTML='<div class="artifact-detail"><p class="artifact-pane-label">交付物预览</p><div class="artifact-empty"><strong>暂时无法预览交付物</strong><span>关闭后重新打开可再试一次。</span></div></div>';
    });
  }

  window.openArtifactCenter=function(){
    var panel=ensurePanel();
    if (typeof window.restoreMacWindow === 'function') window.restoreMacWindow(panel);
    panel.style.display='flex'; state.selectedId=''; state.query=''; state.filter='all'; state.mobileList=false; state.details={}; state.viewRequest++;
    panel.querySelector('#artifactCenterSearch').value='';
    panel.querySelectorAll('[data-filter]').forEach(function(button){button.classList.toggle('active',button.getAttribute('data-filter')==='all');});
    loadDeliveries();
  };
  window.closeArtifactCenter=function(){var panel=document.getElementById('artifactCenterOverlay');if(panel)panel.style.display='none';state.viewRequest++;};
  function injectMenuItem(){
    if(document.getElementById('artifactCenterMenuItem'))return;
    var menu=document.getElementById('toolMenu'); if(!menu)return;
    var item=document.createElement('a'); item.id='artifactCenterMenuItem'; item.href='javascript:void(0)';
    item.innerHTML='<svg viewBox="0 0 24 24" width="14" height="14" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="margin-right:6px;vertical-align:middle;"><path d="M21 8v13H3V8"/><path d="M1 3h22v5H1z"/><path d="M10 12h4"/></svg><span data-i18n="artifactCenter">交付物</span>';
    if (window.openKittyT) item.querySelector('span').textContent = window.openKittyT('artifactCenter', '交付物');
    item.onclick=function(event){if(event)event.preventDefault();if(typeof window.closeDropdownMenus==='function')window.closeDropdownMenus();else menu.classList.remove('show');window.openArtifactCenter();return false;};
    menu.insertBefore(item,menu.firstChild);
  }
  if(document.readyState==='loading')document.addEventListener('DOMContentLoaded',injectMenuItem);else injectMenuItem();
})();
