import {Events} from '@wailsio/runtime';
import {APP_NAME,call,preview,type Locale,type Message,type Presentation} from './api';
import {browserLocale,t,message,translate} from './i18n';
import {mountSettings} from './settings';
import './style.css';

type Stage='idle'|'recording'|'asr'|'llm'|'search'|'injecting'|'done'|'skip'|'clarify'|'error'|'cancelled';
type Source={title:string;url:string;published_date?:string;highlights:string[]};
type State={task_id:string;intent:'dictation'|'dialogue';turn:number;question:string;replying:boolean;revision:number;stage:Stage;visible:boolean;raw_text:string;text:string;notice:string;notice_i18n?:Message;copy_message_i18n?:Message;pinned:boolean;hide_at:number;mode:string;hotkey:string;level:number;sources:Source[]};
type ResultIdentity={task:string;turn:number;text:string};
const root=document.querySelector<HTMLDivElement>('#app')!;
let state:State={task_id:'',intent:'dictation',turn:0,question:'',replying:false,revision:0,stage:'idle',visible:false,raw_text:'',text:'',notice:'',pinned:false,hide_at:0,mode:'live',hotkey:'Ctrl+Alt+R',level:0,sources:[]};
document.title=APP_NAME;
if(new URLSearchParams(location.search).get('view')==='settings') mountSettings(root);
else mountPopup();
function popupView(s:State){
 if(s.question)return 'question';
 if(['done','error','skip','cancelled'].includes(s.stage)&&(s.text||s.raw_text))return 'result';
 return 'working';
}
function mountPopup(){
 let locale:Locale=browserLocale();
 root.innerHTML=`<main class="popup" data-view="working" data-stage="idle"><div class="popup-shell">
 <header class="popup-header"><div class="brand"><img class="mark" src="/app-icon.png" width="28" height="28" alt="" aria-hidden="true"><div class="brand-copy"><strong>${APP_NAME}</strong><span class="badge" id="mode-badge"></span></div></div></header>
 <section class="status" aria-live="polite"><div class="status-title"><span id="indicator" class="indicator"></span><h1 id="stage"></h1></div><div id="waveform" class="waveform" hidden><meter id="level" class="level-meter" min="0" max="1" value="0" hidden></meter><div class="wave-bars" aria-hidden="true">${'<span></span>'.repeat(9)}</div></div></section>
 <div id="popup-body" class="popup-body" tabindex="0" data-i18n-aria-label="popup.aria.body">
 <p id="notice" class="notice" role="status" hidden></p>
 <section id="question-section" class="question-panel" hidden><h2 data-i18n="popup.section.question"></h2><p id="question" class="content"></p></section>
 <section id="raw-section" class="text-section raw-section" hidden><h2 id="raw-label"></h2><p id="raw" class="content"></p></section>
 <section id="result-section" class="text-section result-section" hidden><h2 data-i18n="popup.section.result"></h2><p id="result" class="content"></p></section>
 <details id="sources" hidden><summary data-i18n="popup.section.sources"></summary><ul id="source-list"></ul></details>
 </div>
 <p id="bridge-error" class="warning" role="alert" hidden></p><footer id="popup-footer"><div class="footer-meta"><span id="countdown"></span><span id="copy-message" role="status" hidden></span></div><div id="popup-actions" class="popup-actions"><button id="reply" type="button" class="primary reply-button" hidden></button><div class="popup-commands"><button id="settings" type="button" data-i18n="popup.button.settings" data-i18n-aria-label="popup.aria.settings" data-i18n-title="popup.button.settings"></button><button id="close" type="button" data-i18n="popup.button.close" data-i18n-aria-label="popup.aria.close" data-i18n-title="popup.title.close"></button><button id="copy" type="button" disabled hidden data-i18n="popup.button.copy"></button></div></div></footer></div></main>`;
 const el=<T extends HTMLElement>(id:string)=>root.querySelector<T>(`#${id}`)!;
 const body=el('popup-body');
 const panel=root.querySelector<HTMLElement>('.popup')!;
 const bars=Array.from(root.querySelectorAll<HTMLElement>('.wave-bars span'));
 let warningCode='';
 let copied=false;
 function warning(code:string){warningCode=code;el('bridge-error').hidden=false;el('bridge-error').textContent=t(locale,code);}
 function clearCopyFeedback(){copied=false;el('copy-message').hidden=true;el('copy-message').textContent='';}
 let shortcuts={mode:'live',hotkey:'Ctrl+Alt+R',ai_hotkey:'Ctrl+Alt+A'};
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
 // Presentation updates translate chrome only: no synthetic task, follow, or copy reset.
 function refreshChrome(){
  const scroll=body.scrollTop;
  translate(root,locale);
  const stage=state.stage==='recording'&&state.replying?'reply_recording':state.stage==='asr'&&state.replying?'reply_asr':state.stage==='llm'?(state.intent==='dialogue'?'llm_dialogue':'llm_dictation'):['idle','recording','asr','search','injecting','done','skip','clarify','error','cancelled'].includes(state.stage)?state.stage:'unknown';
  el('stage').textContent=t(locale,`popup.stage.${stage}`);
  const mode=state.stage==='idle'?shortcuts.mode:state.mode;
  el('mode-badge').textContent=t(locale,`popup.intent.${state.intent}`)+(mode==='asr_mock_llm'?t(locale,'popup.mode.asr_passthrough'):'');
  el('raw-label').textContent=t(locale,state.turn>1?'popup.section.raw_with_replies':'popup.section.raw');
  el('reply').textContent=t(locale,state.stage==='clarify'?'popup.button.reply_start':state.replying&&state.stage==='recording'?'popup.button.reply_stop':'popup.button.processing');
  const notice=message(locale,state.notice,state.notice_i18n);if(el('notice').textContent!==notice)el('notice').textContent=notice;el('notice').hidden=!notice;
  const level=el<HTMLMeterElement>('level').value;el('level').setAttribute('aria-label',t(locale,'popup.level',{percent:String(Math.round(level*100))}));
  if(warningCode)el('bridge-error').textContent=t(locale,warningCode);
  const copyFeedback=copied?t(locale,'popup.copy.success'):state.copy_message_i18n?message(locale,'',state.copy_message_i18n):'';
  el('copy-message').textContent=copyFeedback;el('copy-message').hidden=!copyFeedback;
  updateCountdown();body.scrollTop=scroll;
 }
 function render(next:State){
  if(next.revision<state.revision)return;
  const newTurn=next.task_id!==state.task_id||next.turn!==state.turn;
  const newQuestion=Boolean(next.question)&&next.question!==state.question;
  if(newTurn||newQuestion){cancelFollow();body.scrollTop=0;}
  if(newTurn)followed=null;
  if(newTurn||next.text!==state.text||next.raw_text!==state.raw_text)clearCopyFeedback();
  state=next;
  panel.dataset.view=popupView(state);panel.dataset.stage=state.stage;
  const questionVisible=state.intent==='dialogue'&&Boolean(state.question);
  el('question-section').hidden=!questionVisible;el('question').textContent=state.question||'';
  const reply=el<HTMLButtonElement>('reply');reply.hidden=!questionVisible;reply.disabled=replyPending||!canReply();
  for(const[id,text]of[['raw',state.raw_text],['result',state.text]]as const){el(id).textContent=text||'';el(`${id}-section`).hidden=!text;}
  const copy=el<HTMLButtonElement>('copy');copy.disabled=!state.text&&!state.raw_text;copy.hidden=copy.disabled;
  el('indicator').className=`indicator ${['asr','llm','search','injecting'].includes(state.stage)?'busy':state.stage}`;
  const recording=state.stage==='recording';el('waveform').hidden=!recording;
  const level=Number.isFinite(state.level)?Math.max(0,Math.min(1,state.level*8)):0;
  const meter=el<HTMLMeterElement>('level');meter.hidden=!recording;meter.value=level;
  const weights=[.28,.5,.72,.9,1,.9,.72,.5,.28];
  bars.forEach((bar,index)=>{bar.style.height=`${Math.round(2+22*level*weights[index])}px`;});
  const sources=state.sources??[];el('sources').hidden=sources.length===0;
  el('source-list').replaceChildren(...sources.map(source=>{const li=document.createElement('li');li.textContent=`${source.title}${source.published_date?'（'+source.published_date+'）':''}\n${source.url}`;return li;}));
  refreshChrome();followResult();
 }
 function updateCountdown(){
  const hotkey=state.hotkey||t(locale,'popup.footer.hotkey');
  const code=state.stage==='idle'?'idle':state.stage==='clarify'?'reply_start':state.stage==='recording'?(state.replying?'reply_stop':'recording_stop'):state.pinned?'pinned':state.hide_at?'hide_after':'';
  el('countdown').textContent=code?t(locale,`popup.footer.${code}`,{hotkey:state.stage==='idle'?shortcuts.hotkey:hotkey,ai_hotkey:shortcuts.ai_hotkey,seconds:String(Math.max(0,Math.ceil((state.hide_at-Date.now())/1000)))}):'';
 }
 async function action(method:string){if(preview){warning('popup.feedback.preview');return;}try{await call(method);}catch{warning('popup.feedback.operation_failed');}}
 let pinningTask='';
 function keepOpen(){
  if(preview||!state.visible||state.pinned||!state.task_id||pinningTask===state.task_id)return;
  const id=state.task_id;pinningTask=id;
  void call('KeepPopupOpen',id).catch(()=>{if(state.task_id===id)warning('popup.feedback.keep_failed');}).finally(()=>{if(pinningTask===id)pinningTask='';});
 }
 for(const event of ['pointerenter','pointerdown','wheel'])root.addEventListener(event,keepOpen,{passive:true});
 el('close').addEventListener('click',()=>void action('ClosePopup'));el('settings').addEventListener('click',()=>void action('ShowSettings'));
 el('reply').addEventListener('click',async()=>{
  if(preview){warning('popup.feedback.preview');return;}
  if(replyPending||!canReply())return;
  const id=state.task_id,turn=state.turn,stop=state.replying&&state.stage==='recording';
  replyPending=true;el<HTMLButtonElement>('reply').disabled=true;
  try{await call('ReplyToClarification',id,turn,stop);}catch{if(state.task_id===id&&state.turn===turn)warning('popup.feedback.reply_failed');}
  finally{replyPending=false;el<HTMLButtonElement>('reply').disabled=!canReply();}
 });
 el('copy').addEventListener('click',async()=>{
  if(preview){warning('popup.feedback.preview');return;}
  const id=state.task_id,turn=state.turn,text=state.text||state.raw_text;
  try{
   await call('CopyResult',id);
   if(state.task_id!==id||state.turn!==turn||(state.text||state.raw_text)!==text)return;
   copied=true;el('copy-message').textContent=t(locale,'popup.copy.success');el('copy-message').hidden=false;
  }catch{if(state.task_id===id&&state.turn===turn)warning('popup.copy.failed');}
 });
 if(preview){warning('popup.feedback.preview');render(state);}
 else {
  const present=(view:Presentation)=>{shortcuts=view;locale=view.locale??locale;refreshChrome();};
  Events.On('task:state',event=>render(event.data as State));
  Events.On('presentation:changed',event=>present(event.data as Presentation));
  call<State>('Snapshot').then(render).catch(()=>warning('popup.feedback.backend_unavailable'));
  call<Presentation>('GetPresentation').then(present).catch(()=>{});
 }
 setInterval(updateCountdown,250);
}
