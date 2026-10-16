#!/usr/bin/env node
const PptxGenJS=require("pptxgenjs"),fs=require("fs");
const T={default:{p:"1976D2",a:"2196F3",a2:"FFA726",a3:"66BB6A",bg:"F5F5F5",cb:"FFFFFF",t:"212121",tl:"757575",w:"FFFFFF"},tech:{p:"2D7FF9",a:"45A0FF",a2:"FF6B6B",a3:"34D399",bg:"F5F7FA",cb:"FFFFFF",t:"1A1A2E",tl:"64748B",w:"FFFFFF"},finance:{p:"003366",a:"0091D5",a2:"D4AF37",a3:"2E7D32",bg:"F5F7FA",cb:"FFFFFF",t:"1A1A1A",tl:"5F6B7A",w:"FFFFFF"},fjord:{p:"2D5D7B",a:"4A90B8",a2:"F2C94C",a3:"E07B39",bg:"F8F9FA",cb:"FFFFFF",t:"1C2833",tl:"5D6D7E",w:"FFFFFF"},bamboo:{p:"8AA899",a:"769687",a2:"D4A76A",a3:"6B9080",bg:"F4F7F5",cb:"FFFFFF",t:"2D3748",tl:"647473",w:"FFFFFF"},peach:{p:"E0B198",a:"D09A7F",a2:"E8C4B0",a3:"C4886E",bg:"FFF8F3",cb:"FFFFFF",t:"3D2E2A",tl:"8B7355",w:"FFFFFF"},sunset:{p:"FF7D3B",a:"FF9A6B",a2:"FFB48F",a3:"E8453C",bg:"FFF8F5",cb:"FFFFFF",t:"2D1B14",tl:"8B5E4F",w:"FFFFFF"}};
function ea(v){if(!v)return[];if(Array.isArray(v))return v;return[v]}

// Smart parser: collect all content, distribute evenly, merge sparse slides
function parse(md){
  let L=md.split("\n"),th="default",lo=null;
  let title=null,subtitle="",sections=[],cur=null;

  for(let i=0;i<L.length;i++){
    let l=L[i].trim();
    if(l.startsWith("@theme")){th=l.split(/\s+/)[1]||"default";continue}
    if(l.startsWith("@layout")){lo=l.split(/\s+/)[1]||null;continue}

    // Title
    if(l.startsWith("# ")&&!l.startsWith("## ")){
      title=l.replace(/^#\s+/,"");
      if(i+1<L.length&&L[i+1].trim().startsWith("## ")) subtitle=L[++i].trim().replace(/^##\s+/,"");
      continue;
    }

    // KPI
    if(l.startsWith("@kpi")){
      let ps=l.split("|").map(s=>s.trim()),ks=[];
      for(let p=1;p<ps.length;p++){let kv=ps[p].split(":");if(kv.length===2)ks.push({vl:kv[0].trim(),lb:kv[1].trim()})}
      if(ks.length) sections.push({ty:"kpi",ks});
      continue;
    }

    // ## Section
    if(l.startsWith("## ")){
      let t2=l.replace(/^##\s+/,""),lo2=t2.toLowerCase();
      if(/谢谢|thank|感谢|q.?&.?a/.test(lo2)){sections.push({ty:"end",ti:t2});cur=null;continue}
      if(/总结|summary|结论|conclusion/.test(lo2)){sections.push({ty:"section",ti:t2});cur=null;continue}
      cur={ty:"c",ti:t2,bl:[],im:[],tb:null,ch:null,vp:""};
      if(lo){cur.ty=lo;lo=null}
      sections.push(cur);
      continue;
    }

    if(!cur){cur={ty:"c",ti:"",bl:[],im:[],tb:null,ch:null,vp:""};sections.push(cur)}

    let im=l.match(/^!\[(.+)?\]\((.+)\)$/);
    if(im){cur.im.push({alt:im[1]||"",path:im[2]});continue}

    if(l.startsWith("@viewpoint")||l.startsWith("@vp")){cur.vp=l.replace(/^@(viewpoint|vp)\s*/,"");continue}

    if(l.startsWith("```")){
      let cl=[],la=l.replace("```","").trim();
      while(i+1<L.length&&!L[i+1].trim().startsWith("```")){i++;cl.push(L[i].trim())}
      i++;
      if(cl.length)try{cur.ch=JSON.parse(cl.join("\n"))}catch(e){}
      continue;
    }

    if(l.startsWith("|")&&l.endsWith("|")){
      let c=l.split("|").slice(1,-1).map(s=>s.trim());
      if(/^[\s\-:|]+$/.test(l.replace(/\|/g,"")))continue;
      if(!cur.tb)cur.tb={hd:c,rw:[]};else cur.tb.rw.push(c);
      continue;
    }

    if(l.startsWith("### ")){cur.bl.push("**"+l.replace(/^###\s+/,"")+"**");continue}
    if(/^[\*\-]\s+/.test(l)||/^\d+[\.\)]\s+/.test(l)){cur.bl.push(l.replace(/^[\*\-]\s+/,"").replace(/^\d+[\.\)]\s+/,""));continue}
    if(l&&!cur.ti)cur.ti=l;
  }

  // Merge sparse sections
  let slides=[];
  if(title) slides.push({ty:"title",ti:title,su:subtitle});
  let pending=null;
  for(let s of sections){
    let score=(s.bl||[]).length+(s.tb?3:0)+(s.ch?3:0)+(s.im||[]).length*2;
    if(s.ty!=="c"||score>=4){
      if(pending&&pending.bl.length>0) slides.push(pending);
      pending=null;
      slides.push(s);
    } else {
      if(!pending) pending={ty:"c",ti:"",bl:[],im:[],tb:null,ch:null,vp:""};
      if(s.ti&&!pending.ti) pending.ti=s.ti; else if(s.ti) pending.bl.push("**"+s.ti+"**");
      if(s.vp&&!pending.vp) pending.vp=s.vp;
      for(let b of(s.bl||[])) pending.bl.push(b);
      if(s.tb&&!pending.tb) pending.tb=s.tb;
      if(s.ch&&!pending.ch) pending.ch=s.ch;
      for(let im of(s.im||[])) pending.im.push(im);
    }
  }
  if(pending&&pending.bl.length>0) slides.push(pending);
  return{slides,theme:th}
}

// ===== Builders =====
function topbar(s,t){s.addShape("rect",{x:0,y:0,w:10,h:.04,fill:{color:t.p}})}
function vpbox(s,t,tx,y){if(!tx)return y;s.addShape("rect",{x:.3,y,w:9.4,h:.52,fill:{color:t.cb},rectRadius:.06});s.addShape("rect",{x:.3,y,w:.04,h:.52,fill:{color:t.p}});s.addText(tx,{x:.6,y,w:8.8,h:.52,fontSize:13,italic:true,color:t.t,fontFace:"Arial",valign:"middle"});return y+.65}
function titlebar(s,t,tx,y){if(!tx)return y;s.addText(tx,{x:.5,y,w:9,h:.52,fontSize:22,bold:true,color:t.p,fontFace:"Arial"});s.addShape("rect",{x:.5,y:y+.54,w:1,h:.03,fill:{color:t.a}});return y+.7}

function addTitle(s,t,d){s.background={fill:t.w};let cw=7.5,ch=2.8,cx=(10-cw)/2,cy=1.4;s.addShape("rect",{x:cx,y:cy,w:cw,h:ch,fill:{color:t.p},rectRadius:.2,shadow:{type:"outer",blur:12,offset:4,color:"000000",opacity:.12}});s.addText(d.ti||"Title",{x:cx+.5,y:cy+.5,w:cw-1,h:1.4,fontSize:34,bold:true,color:t.w,fontFace:"Arial",align:"center",valign:"middle"});if(d.su)s.addText(d.su,{x:cx+.5,y:cy+2,w:cw-1,h:.5,fontSize:14,color:t.w,fontFace:"Arial",align:"center",transparency:25});s.addShape("rect",{x:cx+2,y:cy+ch+.3,w:cw-4,h:.03,fill:{color:t.a}})}
function addSection(s,t,d){s.background={fill:t.p};s.addText(d.ti||"",{x:.8,y:1.8,w:8.4,h:1.2,fontSize:34,bold:true,color:t.w,fontFace:"Arial"});if(d.su)s.addText(d.su,{x:.8,y:3.2,w:8.4,h:.6,fontSize:15,color:t.w,fontFace:"Arial",transparency:25})}
function addKpi(s,t,d){s.background={fill:t.bg};topbar(s,t);let ks=d.ks||[],cw=Math.min(2.8,9/ks.length-.15),y=.5;ks.forEach((kpi,idx)=>{let cx=.5+idx*(cw+.15);s.addShape("rect",{x:cx,y,w:cw,h:2.2,fill:{color:t.cb},rectRadius:.12,shadow:{type:"outer",blur:3,offset:1,color:"000000",opacity:.04}});s.addText(String(kpi.vl),{x:cx,y:y+.3,w:cw,h:1,fontSize:28,bold:true,color:t.p,fontFace:"Arial",align:"center"});s.addText(String(kpi.lb),{x:cx+.15,y:y+1.4,w:cw-.3,h:.6,fontSize:11,color:t.tl,fontFace:"Arial",align:"center"})})}

function addContent(s,t,d){
  s.background={fill:t.bg};topbar(s,t);
  let y=.3;
  y=vpbox(s,t,d.vp,y);
  y=titlebar(s,t,d.ti,y);
  let bl=ea(d.bl),hasImg=(d.im||[]).length>0,hasExtra=!!(d.tb||d.ch||hasImg);
  let textW=hasImg?5.0:9.0, mx=hasExtra?Math.min(bl.length,8):bl.length;

  if(mx<=3&&!hasExtra){
    bl.slice(0,mx).forEach((b,i)=>{
      let cy=y+i*.58;
      s.addShape("rect",{x:.5,y:cy,w:textW,h:.44,fill:{color:t.cb},rectRadius:.05});
      s.addShape("rect",{x:.5,y:cy,w:.04,h:.44,fill:{color:[t.p,t.a,t.a3][i%3]}});
      s.addText(String(b).replace(/\*\*/g,""),{x:.8,y:cy,w:textW-.5,h:.44,fontSize:11,color:t.t,fontFace:"Arial",valign:"middle"});
    });
    y+=mx*.58;
  } else if(mx>0){
    let perCol=Math.ceil(mx/2);
    bl.slice(0,mx).forEach((b,i)=>{
      let col=i<perCol?0:1,row=i<perCol?i:i-perCol,cx=.5+col*4.65,cy=y+row*.45;
      s.addShape("rect",{x:cx,y:cy,w:4.3,h:.36,fill:{color:t.cb},rectRadius:.04});
      s.addShape("ellipse",{x:cx+.1,y:cy+.1,w:.1,h:.1,fill:{color:[t.p,t.a,t.a3,t.a2][row%4]}});
      s.addText(String(b).replace(/\*\*/g,""),{x:cx+.32,y:cy,w:3.75,h:.36,fontSize:9.5,color:t.t,fontFace:"Arial",valign:"middle"});
    });
    y+=Math.ceil(mx/2)*.45;
  }
  y+=.08;

  if(d.tb){
    let hd=d.tb.hd||["Col"],rs=(d.tb.rw||[]).map(r=>ea(r));
    if(hd.length){
      let cw=9/hd.length,tbl=[hd.map(h=>({text:String(h),options:{bold:true,color:t.w,fill:{color:t.p},fontSize:8,fontFace:"Arial",align:"center",valign:"middle"}}))];
      rs.forEach((r,ri)=>tbl.push(r.map(c=>({text:String(c),options:{fontSize:7.5,fontFace:"Arial",fill:{color:ri%2===0?t.w:t.cb},valign:"middle",align:"center"}}))));
      s.addTable(tbl,{x:.5,y,w:9,border:{type:"solid",pt:.4,color:"DADCE0"},rowH:.2,colW:cw,autoPage:true});
    }
  }

  if(d.ch){
    let raw=d.ch.dt||d.ch.data||[{nm:"Data",lb:["A","B","C"],vl:[10,20,30]}];
    if(!Array.isArray(raw))raw=raw.lb&&raw.vl?[{nm:raw.nm||"S",lb:ea(raw.lb),vl:ea(raw.vl)}]:[raw];
    let sr=raw.map(s=>({name:String(s.nm||s.name||"S"),labels:ea(s.lb||s.labels),values:ea(s.vl||s.values)}));
    s.addChart({bar:"bar",line:"line",pie:"pie"}[d.ch.ct||d.ch.chart_type]||"bar",sr,{x:.5,y,w:9,h:2.0,showTitle:false,showLegend:true,legendPos:"b",chartColors:[t.p,t.a3,t.a2,t.a]});
  }

  (d.im||[]).forEach((img,idx)=>{
    if(fs.existsSync(img.path)) s.addImage({path:img.path,x:5.8,y:.8+idx*2.3,w:3.8,h:2.1});
  });
}

function addTwoCol(s,t,d){s.background={fill:t.bg};topbar(s,t);let y=.3;y=vpbox(s,t,d.vp,y);y=titlebar(s,t,d.ti,y);[{it:ea(d.le),x:.5,c:t.a3},{it:ea(d.ri),x:5.3,c:t.a}].forEach(col=>{s.addShape("rect",{x:col.x,y,w:4.2,h:3.2,fill:{color:t.cb},rectRadius:.1});s.addShape("rect",{x:col.x+.08,y:y+.08,w:.04,h:3.04,fill:{color:col.c}});col.it.forEach((item,i)=>{s.addText(String(item).replace(/\*\*/g,""),{x:col.x+.3,y:y+.15+i*.48,w:3.6,h:.4,fontSize:10,color:t.t,fontFace:"Arial"})})})}
function addQuote(s,t,d){s.background={fill:t.w};topbar(s,t);s.addShape("rect",{x:1.5,y:1.3,w:.05,h:2.2,fill:{color:t.a}});s.addText(d.qu||"",{x:2.1,y:1.4,w:6.8,h:1.8,fontSize:20,italic:true,color:t.t,fontFace:"Georgia",valign:"middle"});if(d.au)s.addText("— "+d.au,{x:2.1,y:3.4,w:6.8,h:.35,fontSize:12,color:t.tl,fontFace:"Arial"})}
function addEnd(s,t,d){s.background={fill:t.p};s.addText(d.ti||"Thank You",{x:.5,y:1.8,w:9,h:1.5,fontSize:40,bold:true,color:t.w,fontFace:"Arial",align:"center"});s.addText("Generated by openkitty AI",{x:.5,y:4.5,w:9,h:.4,fontSize:10,color:t.w,fontFace:"Arial",align:"center",transparency:40})}

const B={title:addTitle,c:addContent,two_column:addTwoCol,section:addSection,kpi:addKpi,quote:addQuote,end:addEnd};

function gen(input,outPath,tn){
  let pptx=new PptxGenJS();pptx.layout="LAYOUT_WIDE";pptx.author="openkitty AI";
  let slides,theme;
  if(typeof input==="object"&&input.slides){slides=input.slides;theme=T[input.theme||"default"]||T.default}
  else{let p=parse(String(input));slides=p.slides;theme=T[tn||p.theme||"default"]||T.default}
  slides.forEach((d,i)=>{let s=pptx.addSlide(),b=B[d.ty]||addContent;try{b(s,theme,d)}catch(e){s.addText("Err:"+e.message,{x:1,y:2,w:8,h:1,fontSize:14,color:"EA4335"})}});
  return pptx.writeFile({fileName:outPath})
}
async function main(){let args=process.argv.slice(2);if(args.length<2){console.error("Usage: node generate.js '<md>' <out.pptx> [theme]");process.exit(1)}let input=args[0];try{input=JSON.parse(input)}catch(e){if(fs.existsSync(input))input=fs.readFileSync(input,"utf-8")}let outDir=require("path").dirname(args[1]);if(!fs.existsSync(outDir))fs.mkdirSync(outDir,{recursive:true});try{await gen(input,args[1],args[2]);let st=fs.statSync(args[1]);console.log("PPTX: "+args[1]+" ("+(st.size/1024).toFixed(1)+"KB)")}catch(err){console.error("Failed:",err.message);process.exit(1)}}
main();