import {Events} from '@wailsio/runtime';
import {APP_NAME,call,preview} from './api';
import {mountSettings} from './settings';
import './style.css';

type Stage='idle'|'recording'|'asr'|'llm'|'search'|'injecting'|'done'|'skip'|'clarify'|'error'|'cancelled';
type Source={title:string;url:string;published_date?:string;highlights:string[]};
type State={task_id:string;intent:'dictation'|'dialogue';turn:number;question:string;replying:boolean;revision:number;stage:Stage;visible:boolean;raw_text:string;text:string;notice:string;pinned:boolean;hide_at:number;mode:string;hotkey:string;level:number;sources:Source[]};
type ResultIdentity={task:string;turn:number;text:string};
const labels:Record<Stage,string>={idle:'准备就绪',recording:'正在录音',asr:'识别语音',llm:'整理语义',search:'查证事实',injecting:'正在输入',done:'已输入',skip:'无需输出',clarify:'需要补充信息',error:'输入未完成',cancelled:'已取消'};
const root=document.querySelector<HTMLDivElement>('#app')!;
let state:State={task_id:'',intent:'dictation',turn:0,question:'',replying:false,revision:0,stage:'idle',visible:false,raw_text:'',text:'',notice:'',pinned:false,hide_at:0,mode:'live',hotkey:'Ctrl+Alt+R',level:0,sources:[]};
if(new URLSearchParams(location.search).get('view')==='settings') mountSettings(root);
else mountPopup();
function popupView(s:State){
 if(s.question)return 'question';
 if(['done','error','skip','cancelled'].includes(s.stage)&&(s.text||s.raw_text))return 'result';
 return 'working';
}
function mountPopup(){
 root.innerHTML=`<main class="popup" data-view="working" data-stage="idle"><div class="popup-shell">
 <header class="popup-header"><div class="brand"><img class="mark" src="/app-icon.png" width="28" height="28" alt="" aria-hidden="true"><div class="brand-copy"><strong>${APP_NAME}</strong><span class="badge" id="mode-badge">语音润色</span></div></div><div class="window-actions">
 <button id="settings" class="icon-button" aria-label="打开设置" title="设置"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="M4 7h16M4 17h16M9 4v6M15 14v6"/></svg></button>
 <button id="close" class="icon-button" aria-label="关闭浮窗并取消任务" title="关闭 / 取消"><svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true"><path d="m6 6 12 12M18 6 6 18"/></svg></button></div></header>
 <section class="status" aria-live="polite"><div class="status-title"><span id="indicator" class="indicator"></span><h1 id="stage">准备就绪</h1></div><div id="waveform" class="waveform" hidden><meter id="level" class="level-meter" min="0" max="1" value="0" aria-label="麦克风音量" hidden></meter><div class="wave-bars" aria-hidden="true">${'<span></span>'.repeat(9)}</div></div></section>
 <div id="popup-body" class="popup-body" tabindex="0" aria-label="任务正文">
 <p id="notice" class="notice" role="status" hidden></p>
 <section id="question-section" class="question-panel" hidden><h2>需要补充</h2><p id="question" class="content"></p></section>
 <section id="raw-section" class="text-section raw-section" hidden><h2 id="raw-label">ASR 原文</h2><p id="raw" class="content"></p></section>
 <section id="result-section" class="text-section result-section" hidden><h2>最终文本</h2><p id="result" class="content"></p></section>
 <details id="sources" hidden><summary>搜索证据来源</summary><ul id="source-list"></ul></details>
 </div>
 <p id="bridge-error" class="warning" role="alert" hidden></p><footer id="popup-footer"><div class="footer-meta"><span id="countdown"></span><span id="copy-message" role="status" hidden></span></div><div id="popup-actions" class="popup-actions" hidden><button id="reply" type="button" class="primary reply-button" hidden>语音补充</button><button id="copy" disabled hidden>复制</button></div></footer></div></main>`;
 const el=<T extends HTMLElement>(id:string)=>root.querySelector<T>(`#${id}`)!;
 const body=el('popup-body');
 const panel=root.querySelector<HTMLElement>('.popup')!;
 const bars=Array.from(root.querySelectorAll<HTMLElement>('.wave-bars span'));
 function warning(message:string){el('bridge-error').hidden=false;el('bridge-error').textContent=message;}
 function clearCopyFeedback(){el('copy-message').hidden=true;el('copy-message').textContent='';}
 let shortcuts={hotkey:'Ctrl+Alt+R',ai_hotkey:'Ctrl+Alt+A'};
 let replyPending=false;
 let followed:ResultIdentity|null=null;
 let followFrame:number|null=null;
 function cancelFollow(){if(followFrame!==null)cancelAnimationFrame(followFrame);followFrame=null;}
 function matchesResult(key:ResultIdentity){return state.task_id===key.task&&state.turn===key.turn&&state.text===key.text;}
 function followResult(){
  if(!state.visible||!['done','error'].includes(state.stage)||!state.text)return;
  if(followed&&matchesResult(followed))return;
  cancelFollow();const key={task:state.task_id,turn:state.turn,text:state.text};followed=key;
  followFrame=requestAnimationFrame(()=>{
   followFrame=null;
   if(!matchesResult(key)||!state.visible||!['done','error'].includes(state.stage))return;
   const viewport=body.getBoundingClientRect(),result=el('result').getBoundingClientRect();
   const target=body.scrollTop+result.bottom-(viewport.top+body.clientTop+body.clientHeight)+12;
   body.scrollTop=Math.max(0,Math.min(body.scrollHeight-body.clientHeight,target));
  });
 }
 for(const event of ['wheel','touchstart','pointerdown'])body.addEventListener(event,cancelFollow,{passive:true});
 body.addEventListener('keydown',event=>{if(['ArrowUp','ArrowDown','PageUp','PageDown','Home','End',' '].includes(event.key))cancelFollow();});
 const canReply=()=>state.intent==='dialogue'&&(state.stage==='clarify'||(state.replying&&state.stage==='recording'));
 function render(next:State){
  if(next.revision<state.revision)return;
  const newTurn=next.task_id!==state.task_id||next.turn!==state.turn;
  const newQuestion=Boolean(next.question)&&next.question!==state.question;
  if(newTurn||newQuestion){cancelFollow();body.scrollTop=0;}
  if(newTurn)followed=null;
  if(newTurn||next.text!==state.text||next.raw_text!==state.raw_text)clearCopyFeedback();
  state=next;
  panel.dataset.view=popupView(state);panel.dataset.stage=state.stage;
  el('stage').textContent=state.stage==='recording'&&state.replying?'正在录制补充':state.stage==='asr'&&state.replying?'识别补充语音':state.stage==='llm'?(state.intent==='dialogue'?'生成所需文本':'正在润色文本'):labels[state.stage]??'未知状态';
  el('mode-badge').textContent=`${state.intent==='dialogue'?'AI 对话':'语音润色'}${state.mode==='asr_mock_llm'?' · ASR 直通':''}`;
  el('raw-label').textContent=state.turn>1?'语音记录（含补充）':'ASR 原文';
  const questionVisible=state.intent==='dialogue'&&Boolean(state.question);
  el('question-section').hidden=!questionVisible;el('question').textContent=state.question||'';
  const reply=el<HTMLButtonElement>('reply');reply.hidden=!questionVisible;reply.disabled=replyPending||!canReply();reply.textContent=state.stage==='clarify'?'语音补充':state.replying&&state.stage==='recording'?'结束补充':'处理中……';
  el('notice').textContent=state.notice;el('notice').hidden=!state.notice;
  for(const[id,text]of[['raw',state.raw_text],['result',state.text]]as const){el(id).textContent=text||'';el(`${id}-section`).hidden=!text;}
  const copy=el<HTMLButtonElement>('copy');copy.disabled=!state.text&&!state.raw_text;copy.hidden=copy.disabled;
  el('popup-actions').hidden=copy.hidden&&reply.hidden;
  el('indicator').className=`indicator ${['asr','llm','search','injecting'].includes(state.stage)?'busy':state.stage}`;
  const recording=state.stage==='recording';el('waveform').hidden=!recording;
  const level=Number.isFinite(state.level)?Math.max(0,Math.min(1,state.level*8)):0;
  const meter=el<HTMLMeterElement>('level');meter.hidden=!recording;meter.value=level;
  const weights=[.28,.5,.72,.9,1,.9,.72,.5,.28];
  bars.forEach((bar,index)=>{bar.style.height=`${Math.round(2+22*level*weights[index])}px`;});
  const sources=state.sources??[];el('sources').hidden=sources.length===0;
  el('source-list').replaceChildren(...sources.map(source=>{const li=document.createElement('li');li.textContent=`${source.title}${source.published_date?'（'+source.published_date+'）':''}\n${source.url}`;return li;}));
  updateCountdown();followResult();
 }
 function updateCountdown(){const text=state.stage==='idle'?`润色 ${shortcuts.hotkey} / AI ${shortcuts.ai_hotkey}`:state.stage==='clarify'?`${state.hotkey||'快捷键'} 开始补充`:state.stage==='recording'?`${state.hotkey||'快捷键'} ${state.replying?'结束补充':'结束录音'}`:state.pinned?'保持显示':state.hide_at?`${Math.max(0,Math.ceil((state.hide_at-Date.now())/1000))} 秒后隐藏`:'';el('countdown').textContent=text;}
 async function action(method:string){if(preview){warning('只读预览');return;}try{await call(method);}catch{warning('操作失败，请重试');}}
 let pinningTask='';
 function keepOpen(){
  if(preview||!state.visible||state.pinned||!state.task_id||pinningTask===state.task_id)return;
  const id=state.task_id;pinningTask=id;
  void call('KeepPopupOpen',id).catch(()=>{if(state.task_id===id)warning('保持显示失败，请及时复制');}).finally(()=>{if(pinningTask===id)pinningTask='';});
 }
 for(const event of ['pointerenter','pointerdown','wheel'])root.addEventListener(event,keepOpen,{passive:true});
 el('close').addEventListener('click',()=>void action('ClosePopup'));el('settings').addEventListener('click',()=>void action('ShowSettings'));
 el('reply').addEventListener('click',async()=>{
  if(preview){warning('只读预览');return;}
  if(replyPending||!canReply())return;
  const id=state.task_id,turn=state.turn,stop=state.replying&&state.stage==='recording';
  replyPending=true;el<HTMLButtonElement>('reply').disabled=true;
  try{await call('ReplyToClarification',id,turn,stop);}catch{if(state.task_id===id&&state.turn===turn)warning('补充录音失败，请重试');}
  finally{replyPending=false;el<HTMLButtonElement>('reply').disabled=!canReply();}
 });
 el('copy').addEventListener('click',async()=>{
  if(preview){warning('只读预览');return;}
  const id=state.task_id,turn=state.turn,text=state.text||state.raw_text;
  try{
   await call('CopyResult',id);
   if(state.task_id!==id||state.turn!==turn||(state.text||state.raw_text)!==text)return;
   el('copy-message').textContent='已复制';el('copy-message').hidden=false;
  }catch{if(state.task_id===id&&state.turn===turn)warning('复制失败，请重试');}
 });
 if(preview){warning('只读预览');render(state);}
 else {
  Events.On('task:state',event=>render(event.data as State));
  call<State>('Snapshot').then(render).catch(()=>warning('无法连接后端，请重启程序'));
  call<{mode:string;hotkey:string;ai_hotkey:string}>('GetPresentation').then(view=>{shortcuts=view;if(state.stage==='idle')render({...state,mode:view.mode,hotkey:view.hotkey});}).catch(()=>{});
 }
 setInterval(updateCountdown,250);
}
