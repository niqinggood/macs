(function () {
    'use strict';

    var canvasRenderer;
    function loadCanvasRenderer() {
        if (window.html2canvas) return Promise.resolve(window.html2canvas);
        if (!canvasRenderer) canvasRenderer=new Promise(function(resolve,reject){
            var script=document.createElement('script');
            var timeout=setTimeout(function(){script.remove();canvasRenderer=null;reject(new Error('加载导出组件超时，请重试。'));},20000);
            script.src='/web/html2canvas.min.js';
            script.onload=function(){clearTimeout(timeout);resolve(window.html2canvas);};
            script.onerror=function(){clearTimeout(timeout);canvasRenderer=null;script.remove();reject(new Error('加载导出组件失败，请重试。'));};
            document.head.appendChild(script);
        });
        return canvasRenderer;
    }
    window.installConversationExportActions=function(target){
        target.document.querySelector('.btn-img').addEventListener('click',async function(){
            var button=this, bar=target.document.querySelector('.print-bar');
            button.disabled=true;
            try {
                var render=await loadCanvasRenderer();
                await Promise.all(Array.from(target.document.images).map(async function(image){if(image.decode)await image.decode();if(!image.naturalWidth)throw imageError();}));
                if(target.document.fonts)await target.document.fonts.ready;
                bar.style.display='none';target.document.body.classList.add('img-mode');
                var canvas=await render(target.document.body,{scale:1,backgroundColor:'#fff',logging:false,allowTaint:false,useCORS:false});
                var blob=await new Promise(function(resolve,reject){canvas.toBlob(function(value){if(value)resolve(value);else reject(new Error('导出内容过长，请使用打印 PDF。'));},'image/png');});
                var url=target.URL.createObjectURL(blob), link=target.document.createElement('a');
                link.download='chat_export.png';link.href=url;target.document.body.appendChild(link);link.click();link.remove();
                setTimeout(function(){target.URL.revokeObjectURL(url);},60000);
            } catch(error){if(!target.closed)target.alert(error.message || '导出失败，请重试。');}
            finally {if(!target.closed){bar.style.display='';target.document.body.classList.remove('img-mode');button.disabled=false;}}
        });
    };

    function imageError() {
        return new Error(window.openKittyT('exportImageReadFailed', '图片读取失败，请确认已登录后重试。'));
    }
    async function inlineImage(image, requestID) {
        var source=image.getAttribute('src') || image.dataset.src;
        if(!source) throw imageError();
        if(!/^data:image\//i.test(source)) {
            var target;
            try {target=new URL(source,location.href);} catch(error) {throw imageError();}
            if(!['http:','https:'].includes(target.protocol) || target.username || target.password) throw imageError();
            var path=requestID && image.classList.contains('generation-result-image') ? '/api/conversation/media/'+encodeURIComponent(requestID)+'/image' : target.href;
            var controller=new AbortController(), timeout=setTimeout(function(){controller.abort();},25000);
            try {
                var response=await fetch(path,{credentials:new URL(path,location.href).origin===location.origin?'same-origin':'omit',redirect:'error',cache:'no-store',signal:controller.signal});
                if(!response.ok) throw imageError();
                var blob=await response.blob();
                if(!/^image\/(png|jpeg|gif|webp|svg\+xml)$/i.test(blob.type) || !blob.size || blob.size>20*1024*1024) throw imageError();
                source=await new Promise(function(resolve,reject){var reader=new FileReader();reader.onload=function(){resolve(reader.result);};reader.onerror=function(){reject(imageError());};reader.readAsDataURL(blob);});
            } catch(error) {throw imageError();} finally {clearTimeout(timeout);}
        }
        image.src=source;image.removeAttribute('srcset');image.removeAttribute('data-src');image.removeAttribute('crossorigin');image.setAttribute('loading','eager');
    }
    window.prepareConversationExportMessage=async function(message) {
        var role=message.classList.contains('user')?'user':'assistant', bubble=message.querySelector('.message-bubble');
        var content=bubble?.querySelector('.message-content') || bubble || message;
        var clone=content.cloneNode(true);
        clone.querySelectorAll('button,script,input,textarea,progress').forEach(function(node){node.remove();});
        clone.querySelectorAll('details').forEach(function(node){
            var section=document.createElement('section');section.className='generation-export-lyrics';
            var summary=node.querySelector('summary');
            if(summary){var label=document.createElement('p');label.textContent=summary.textContent;section.append(label);summary.remove();}
            while(node.firstChild)section.append(node.firstChild);
            node.replaceWith(section);
        });
        for(var image of clone.querySelectorAll('img')) await inlineImage(image,message.dataset.generationRequest);
        clone.querySelectorAll('audio,video').forEach(function(player){
            var music=player.tagName==='AUDIO', source=player.getAttribute('src'), target;
            try {target=new URL(source,location.href);} catch(error) {return;}
            if(!['http:','https:'].includes(target.protocol) || target.username || target.password)return;
            player.src=target.href;
            var card=document.createElement('div');card.className='generation-export-media';
            var title=document.createElement('strong');title.textContent=(music?'♫ ':'▶ ')+(message.dataset.generationTitle || window.openKittyT(music?'composerTabMusic':'composerTabVideo',music?'音乐':'视频'));
            var link=document.createElement('a');link.href=target.href;link.target='_blank';link.rel='noopener noreferrer';link.textContent=window.openKittyT(music?'exportPlayMusic':'exportPlayVideo',music?'播放 / 下载音乐':'播放 / 下载视频');
            if(!player.closest('.generation-music-card'))card.append(title);
            card.append(link);player.before(card);
        });
        clone.querySelectorAll('a[href]').forEach(function(link){
            try {var target=new URL(link.getAttribute('href'),location.href);if(['http:','https:'].includes(target.protocol)&&!target.username&&!target.password)link.href=target.href;else link.removeAttribute('href');}catch(error){link.removeAttribute('href');}
        });
        window.redactAccountInfoInExportDOM(clone);
        clone.querySelectorAll('*').forEach(function(node){
            Array.from(node.attributes).forEach(function(attribute){
                if(/^on/i.test(attribute.name)){node.removeAttribute(attribute.name);return;}
                if(attribute.name==='src' && /^data:image\//i.test(attribute.value))return;
                node.setAttribute(attribute.name,window.redactAccountInfoForExport(attribute.value));
            });
        });
        return {role:role,content:clone.innerHTML,music:!!clone.querySelector('.generation-music-card'),name:window.redactAccountInfoForExport(bubble?.querySelector('.agent-name')?.textContent.trim() || (role==='user'?'我':'AI')),
                time:window.redactAccountInfoForExport(bubble?.querySelector('.message-info')?.textContent.trim() || '')};
    };
})();
