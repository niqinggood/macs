(function () {
    'use strict';
    var COPY = {
        zh: {title:'记忆',close:'关闭记忆窗口',context:'当前记忆',dirty:'记忆中有未保存的编辑内容。可以继续查看当前记忆，或明确放弃后切换到新 助手。',keep:'继续查看当前记忆',discard:'放弃未保存内容并切换',saving:'记忆正在保存，请稍后再切换。',clean:'未保存内容已处理，可以切换到新的 助手。',switch:'切换到新记忆',loading:'正在打开记忆…',failed:'记忆暂时无法打开，请重试。',retry:'重试',theme:'切换主题',language:'中文 / EN',font:'字体大小'},
        en: {title:'Memory',close:'Close memory window',context:'Current memory',dirty:'Memory has unsaved edits. Keep the current view, or explicitly discard them to switch to the new assistant or category.',keep:'Keep current memory',discard:'Discard unsaved edits and switch',saving:'Memory is being saved. Wait before switching.',clean:'Unsaved edits have been handled. You can switch to the new assistant or category.',switch:'Switch to new memory',loading:'Opening memory…',failed:'Memory could not be opened. Try again.',retry:'Retry',theme:'Switch theme',language:'中文 / EN',font:'Font size'}
    };
    var overlay,surface,header,title,frame,notice,actions,context=null,pending=null,returnFocus=null,syncing=false,ready=false;
    function language(){return String(window.openKittyLanguage||document.documentElement.lang||'zh').toLowerCase().indexOf('en')===0?'en':'zh';}
    function t(key){return COPY[language()][key]||key;}
    function node(tag,cls,text){var item=document.createElement(tag);if(cls)item.className=cls;if(text!=null)item.textContent=text;return item;}
    function button(label,handler,cls){var item=node('button',cls||'mp-button',label);item.type='button';item.addEventListener('click',handler);return item;}
    function currentContext(category){var selected=document.getElementById('agentSelect');return {agent:(selected&&selected.value)||window._defaultAgentName||'',category:category||''};}
    function same(a,b){return a&&b&&a.agent===b.agent;}
    function dirty(){try{return frame&&frame.contentWindow&&typeof frame.contentWindow.getMacsMemoryDirtyState==='function'?frame.contentWindow.getMacsMemoryDirtyState():{dirty:false,saving:false};}catch(error){return {dirty:true,saving:false};}}
    function active(){return overlay&&overlay.classList.contains('active')&&!overlay.classList.contains('mac-window-minimized');}
    function syncAppearance(){
        if(!frame||!ready||syncing)return;
        syncing=true;
        try {
            var win=frame.contentWindow,doc=frame.contentDocument;
            if(typeof win.openKittyApplyLanguage==='function'&&win.openKittyLanguage!==window.openKittyLanguage)win.openKittyApplyLanguage(language());
            var parentTheme=document.getElementById('theme-style'),childTheme=doc.getElementById('theme-style');
            if(parentTheme&&childTheme&&Boolean(parentTheme.textContent.trim())!==Boolean(childTheme.textContent.trim())){try{if(typeof win.toggleTheme==='function')win.toggleTheme();}catch(error){childTheme.textContent=parentTheme.textContent;}}
            var percent=document.documentElement.dataset.fontSize||'110';
            if(doc.documentElement.dataset.fontSize!==percent)win.dispatchEvent(new win.StorageEvent('storage',{key:'macs.ui.fontSize',newValue:percent}));
        } catch(error) {} finally {syncing=false;}
    }
    function render(){
        if(!overlay)return;title.textContent=t('title');frame.title=t('title');header.querySelector('.close-modal').setAttribute('aria-label',t('close'));
        var themeButton=header.querySelector('[data-memory-panel-theme]'),fontButton=header.querySelector('[data-memory-panel-font]');themeButton.title=t('theme');themeButton.setAttribute('aria-label',t('theme'));fontButton.title=t('font');fontButton.setAttribute('aria-label',t('font'));header.querySelector('[data-memory-panel-language]').textContent=t('language');
        var status=dirty();notice.hidden=!pending;notice.textContent=pending?t(status.saving?'saving':status.dirty?'dirty':'clean'):'';actions.replaceChildren();
        if(pending&&!status.saving){actions.append(button(t('keep'),function(){pending=null;render();frame.focus();}),button(t(status.dirty?'discard':'switch'),function(){var target=pending;if(dirty().saving){render();return;}pending=null;loadContext(target);},'mp-button mp-primary'));}
    }
    function childKeys(event){
        if(!active()||event.isComposing)return;
        var doc=frame.contentDocument;if(doc.querySelector('dialog[open]'))return;
        if(event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();window.closeMemoryPanel();return;}
        if(event.key!=='Tab')return;
        var items=Array.from(doc.querySelectorAll('a[href],button,input,textarea,select,summary,[tabindex="0"]')).filter(function(item){return !item.disabled&&item.getClientRects().length;});
        var index=items.indexOf(doc.activeElement),parentItems=parentFocusable();
        if(event.shiftKey&&index===0){event.preventDefault();parentItems.filter(function(item){return item!==frame;}).slice(-1)[0].focus();}
        else if(!event.shiftKey&&items.length&&index===items.length-1){event.preventDefault();parentItems[0].focus();}
    }
    function ensure(){
        if(overlay)return;overlay=node('div','modal memory-panel-modal');overlay.id='memoryPanelModal';overlay.setAttribute('role','dialog');overlay.setAttribute('aria-modal','true');overlay.setAttribute('aria-hidden','true');
        surface=node('div','modal-content mp-surface');header=node('header','modal-header');title=node('h2');title.id='memoryPanelTitle';overlay.setAttribute('aria-labelledby',title.id);header.appendChild(title);
        var controls=node('div','mp-appearance');var font=button('Aa',function(event){event.stopPropagation();var trigger=document.getElementById('fontSizeToggle');if(trigger)trigger.click();},'mp-appearance-button');font.dataset.memoryPanelFont='';font.setAttribute('aria-label',t('font'));
        var theme=button('◐',function(){if(typeof window.toggleTheme==='function')window.toggleTheme();},'mp-appearance-button');theme.dataset.memoryPanelTheme='';theme.setAttribute('aria-label',t('theme'));
        var lang=button(t('language'),function(){if(typeof window.toggleLanguage==='function')window.toggleLanguage();},'mp-appearance-button');lang.dataset.memoryPanelLanguage='';controls.append(font,theme,lang);header.append(controls,button('×',window.closeMemoryPanel,'close-modal'));
        notice=node('p','mp-notice');notice.id='memoryPanelNotice';notice.setAttribute('role','status');notice.hidden=true;actions=node('div','mp-context-actions');frame=node('iframe','mp-frame');frame.id='memoryPanelFrame';frame.title=t('title');
        frame.addEventListener('load',function(){ready=true;syncAppearance();try{frame.contentDocument.addEventListener('keydown',childKeys,true);frame.contentDocument.addEventListener('openkitty:languagechange',syncAppearance);frame.contentDocument.addEventListener('macs:memory-statechange',function(){if(pending)render();});}catch(error){}render();});
        surface.append(header,notice,actions,frame);overlay.appendChild(surface);overlay.addEventListener('click',function(event){if(event.target===overlay)window.closeMemoryPanel();});document.body.appendChild(overlay);
        var style=document.getElementById('theme-style');if(style)new MutationObserver(syncAppearance).observe(style,{childList:true,subtree:true,characterData:true});render();
    }
    function loadContext(next){context=next;ready=false;var params=new URLSearchParams({embed:'1'});if(next.agent)params.set('agent',next.agent);if(next.category)params.set('category',next.category);frame.src='/web/memory.html?'+params;render();}
    function parentFocusable(){return Array.from(surface.querySelectorAll('button,iframe')).filter(function(item){return !item.disabled&&item.getClientRects().length;});}
    window.closeMemoryPanel=function(){
        if(!overlay)return;if(typeof window.restoreMacWindow==='function')window.restoreMacWindow(overlay);overlay.classList.remove('active');overlay.setAttribute('aria-hidden','true');pending=null;render();var target=returnFocus;if(!target||!target.isConnected||!target.getClientRects().length)target=document.querySelector('.memory-nav-link');if(target)target.focus({preventScroll:true});
    };
    window.openMemoryPanel=function(category){
        category=['short','long','preferences','project','global','knowledge','history'].includes(category)?category:'';
        ensure();if(!overlay.classList.contains('active'))returnFocus=document.activeElement;if(typeof window.restoreMacWindow==='function')window.restoreMacWindow(overlay);overlay.classList.add('active');overlay.setAttribute('aria-hidden','false');
        var next=currentContext(category);if(!context)loadContext(next);else if(!same(context,next)){var status=dirty();if(status.dirty||status.saving){pending=next;render();}else{pending=null;loadContext(next);}}else{
            pending=null;
            if(category){
                if(!ready){loadContext(next);}
                else{try{var switched=frame.contentWindow.macsMemorySelectCategory(category);if(!switched){var currentState=dirty();if(currentState.dirty||currentState.saving)pending=next;}}catch(error){}}
            }
            render();syncAppearance();
        }
        var target=pending?actions.querySelector('button'):header.querySelector('button');if(target)target.focus({preventScroll:true});
    };
    document.addEventListener('keydown',function(event){
        if(!active()||event.isComposing)return;var fontMenu=document.querySelector('.font-size-menu');if(fontMenu&&!fontMenu.hidden)return;
        if(event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();window.closeMemoryPanel();return;}
        if(event.key!=='Tab')return;var items=parentFocusable(),dock=document.querySelector('.mac-window-dock');if(dock&&!dock.hidden)items=items.concat(Array.from(dock.querySelectorAll('button')).filter(function(item){return item.getClientRects().length;}));var index=items.indexOf(document.activeElement);if(event.shiftKey&&index<=0){event.preventDefault();items[items.length-1].focus();}else if(!event.shiftKey&&(index<0||index===items.length-1)){event.preventDefault();items[0].focus();}
    },true);
    document.addEventListener('openkitty:languagechange',function(){render();syncAppearance();});document.addEventListener('openkitty:fontsizechange',syncAppearance);
})();
