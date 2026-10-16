package network

import (
	"context"
	"github.com/chromedp/chromedp"
	"time"
)

// FetchSnapshot renders the page and extracts DOM structure as an accessibility tree.
func FetchSnapshot(ctx context.Context, urlStr string) (string, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU, chromedp.NoSandbox, chromedp.Headless,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("window-size", "1440,900"),
	)
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()
	ctx, cancel = chromedp.NewContext(allocCtx)
	defer cancel()

	var snapshot string
	err := chromedp.Run(ctx,
		chromedp.Navigate(urlStr),
		chromedp.Sleep(3*time.Second),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1*time.Second),
		chromedp.Evaluate(`(()=>{
	let out=[];
	function walk(el,depth){
		if(!el||el.nodeType!==1)return;
		let tag=el.tagName.toLowerCase();
		if(tag==='script'||tag==='style'||tag==='noscript'||tag==='meta'||tag==='link')return;
		let indent='  '.repeat(depth);
		let role=el.getAttribute('role')||'';
		let cls=(el.className&&typeof el.className==='string')?'.'+el.className.split(/\s+/).filter(c=>c.length<20).slice(0,3).join('.') :'';
		let text='';
		for(let c of el.childNodes){
			if(c.nodeType===3){let t=c.textContent.trim();if(t)text+=t+' '}
		}
		text=text.trim().substring(0,100);
		let label=el.getAttribute('aria-label')||el.getAttribute('title')||'';
		let id=el.id?'#'+el.id:'';
		let href=el.getAttribute('href')||'';
		let parts=[];
		if(role)parts.push(role);
		parts.push(tag+id+cls);
		if(label)parts.push('"'+label.substring(0,60)+'"');
		if(href)parts.push('->'+href.substring(0,60));
		if(text)parts.push('"'+text+'"');
		out.push(indent+'- '+parts.join(' '));
		for(let c of el.children)walk(c,depth+1);
	}
	walk(document.body,0);
	return out.join('\n');
})()`, &snapshot),
	)
	if err != nil {
		return "", err
	}
	if len(snapshot) == 0 {
		return "(empty page)", nil
	}
	return "rootwebarea \"" + urlStr + "\"\n" + snapshot, nil
}
