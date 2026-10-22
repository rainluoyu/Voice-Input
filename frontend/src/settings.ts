import {APP_NAME, call, preview, defaultConfig, type Config, type SettingsView, type ActionResult, type Locale, type UILanguage, type Message, type PromptDefaults} from './api';
import {browserLocale, resolveLocale, t, message, translate} from './i18n';

type Device = {id:string;name:string;name_i18n?:Message};
type Service = 'asr'|'llm'|'exa';
type PromptKind = 'dictation'|'dialogue';
const services:Service[] = ['asr','llm','exa'];
const promptKinds:PromptKind[] = ['dictation','dialogue'];
const textLimit = 16000;
const text = (key:string) => `<span data-i18n="${key}"></span>`;

export function mountSettings(root:HTMLElement) {
 let baseline = defaultConfig();
 let locale=browserLocale();
 let systemLocale=locale;
 let promptDefaultsByLocale:Record<Locale,PromptDefaults>={en:{dictation:'',dialogue:''},'zh-CN':{dictation:'',dialogue:''}};
 const promptSources:Record<PromptKind,'default'|'custom'>={dictation:'default',dialogue:'default'};
 const feedback=new Map<string,{text:string;identity?:Message}>();
 const deviceNames=new WeakMap<HTMLOptionElement,Device>();
 const keySaved:Record<Service,boolean>={asr:false,llm:false,exa:false};
 let loaded = false;
 let saving = false;
 const busy = new Set<Service>();
 root.innerHTML = `<main class="settings">
 <header class="settings-header"><img class="mark" src="/app-icon.png" width="28" height="28" alt="" aria-hidden="true"><h1>${APP_NAME}</h1></header>
 <nav class="settings-tabs" role="tablist" data-i18n-aria-label="settings.ui.tabs" aria-orientation="vertical">
 ${tab('input','settings.ui.tab.input',true)}${tab('general','settings.ui.tab.general')}${tab('models','settings.ui.tab.models')}${tab('prompts','settings.ui.tab.prompts')}${tab('network','settings.ui.tab.network')}
 </nav>
 <form id="preferences" class="settings-body" novalidate>
 <p class="warning" id="bridge-error" role="alert" hidden></p><p class="warning" id="startup-error" role="alert" hidden></p>
 <fieldset id="settings-fields" data-i18n-aria-label="settings.ui.fields" disabled>
 <section id="panel-input" class="settings-panel" role="tabpanel" aria-labelledby="tab-input" data-settings-panel="input">
 <section class="settings-group"><h2>${text('settings.ui.input')}</h2><div class="form-grid">
 <label class="span-two" for="ui-language">${text('settings.ui.ui_language')}<select id="ui-language"><option value="auto" data-i18n="settings.ui.language.auto"></option><option value="zh-CN" data-i18n="settings.ui.language.zh-CN"></option><option value="en" data-i18n="settings.ui.language.en"></option></select><small data-i18n="settings.ui.language.hint"></small></label>
 <label class="span-two" for="mode">${text('settings.ui.mode')}<select id="mode"><option value="live">ASR + LLM</option><option value="asr_mock_llm" data-i18n="settings.ui.mode.direct"></option></select></label>
 <label for="hotkey">${text('settings.ui.hotkey')}<input id="hotkey" required maxlength="80" placeholder="Ctrl+Alt+R"></label>
 <label for="ai-hotkey">${text('settings.ui.ai_hotkey')}<input id="ai-hotkey" required maxlength="80" placeholder="Ctrl+Alt+A"></label>
 <label for="max-seconds">${text('settings.ui.max_seconds')}<input id="max-seconds" type="number" min="1" max="300" required></label>
 <div class="span-two"><label for="device">${text('settings.ui.microphone')}</label><div class="inline-field"><select id="device"><option value="default" data-i18n="settings.ui.device.default"></option></select><button id="refresh-devices" type="button" data-i18n="settings.ui.refresh_devices"></button></div><small id="device-message" role="status" hidden></small></div>
 </div></section>
 <section class="settings-group"><h2><label for="profile-custom">${text('settings.ui.background')}</label></h2><textarea id="profile-custom" class="background-input" rows="8" aria-describedby="profile-count" data-i18n-placeholder="settings.ui.background.placeholder"></textarea><output id="profile-count" class="character-count" for="profile-custom"></output></section>
 </section>
 <section id="panel-general" class="settings-panel" role="tabpanel" aria-labelledby="tab-general" data-settings-panel="general" hidden>
 <section class="settings-group"><h2>${text('settings.ui.general')}</h2><div class="form-grid">
 <div class="span-two"><label class="check" for="start-with-windows"><input id="start-with-windows" type="checkbox" aria-describedby="start-with-windows-hint">${text('settings.ui.general.start_with_windows')}</label><small id="start-with-windows-hint" data-i18n="settings.ui.general.start_with_windows.hint"></small></div>
 <div class="span-two"><label class="check" for="start-as-admin"><input id="start-as-admin" type="checkbox" aria-describedby="start-as-admin-hint" disabled>${text('settings.ui.general.start_as_admin')}</label><small id="start-as-admin-hint" data-i18n="settings.ui.general.start_as_admin.hint"></small></div>
 <div class="span-two"><label class="check" for="auto-copy"><input id="auto-copy" type="checkbox" aria-describedby="auto-copy-hint">${text('settings.ui.general.auto_copy')}</label><small id="auto-copy-hint" data-i18n="settings.ui.general.auto_copy.hint"></small></div>
 <div class="span-two"><label class="check" for="mute-while-recording"><input id="mute-while-recording" type="checkbox" aria-describedby="mute-while-recording-hint">${text('settings.ui.general.mute_while_recording')}</label><small id="mute-while-recording-hint" data-i18n="settings.ui.general.mute_while_recording.hint"></small></div>
 <label class="span-two" for="auto-hide-seconds">${text('settings.ui.general.auto_hide_seconds')}<input id="auto-hide-seconds" type="number" min="1" max="3600" step="1" required aria-describedby="auto-hide-seconds-hint"><small id="auto-hide-seconds-hint" data-i18n="settings.ui.general.auto_hide_seconds.hint"></small></label>
 </div></section>
 </section>
 <section id="panel-models" class="settings-panel" role="tabpanel" aria-labelledby="tab-models" data-settings-panel="models" hidden>
 <section class="settings-group"><h2>${text('settings.ui.asr')}</h2><div class="form-grid">
 <label class="span-two" for="asr-provider">${text('settings.ui.asr.protocol')}<select id="asr-provider"><option value="openai_compatible">OpenAI-compatible</option><option value="mimo">MiMo ASR</option></select></label>
 <label class="span-two" for="asr-endpoint">${text('settings.ui.asr.endpoint')}<input id="asr-endpoint" type="url"></label>
 <label for="asr-model">${text('settings.ui.model')}<input id="asr-model" maxlength="200" autocomplete="off"></label><label for="asr-language">${text('settings.ui.asr.language')}<input id="asr-language" maxlength="20" placeholder="zh"></label>
 <label for="asr-timeout">${text('settings.ui.timeout')}<input id="asr-timeout" type="number" min="5" max="180" required></label></div>${credential('asr','ASR')}</section>
 <section class="settings-group"><h2>${text('settings.ui.llm')}</h2><div class="form-grid">
 <label class="span-two" for="llm-base"><span>Base URL</span><input id="llm-base" type="url" data-i18n-placeholder="settings.ui.llm.base.placeholder"></label><label for="llm-model">${text('settings.ui.model')}<input id="llm-model" maxlength="200" autocomplete="off"></label>
 <label for="output-format">${text('settings.ui.output_format')}<select id="output-format"><option value="json_schema">json_schema</option><option value="json_object">json_object</option></select></label>
 </div>${credential('llm','LLM')}</section>
 </section>
 <section id="panel-prompts" class="settings-panel" role="tabpanel" aria-labelledby="tab-prompts" data-settings-panel="prompts" hidden>
 <div class="prompt-tabs" role="tablist" data-i18n-aria-label="settings.ui.prompt.tabs" aria-orientation="horizontal">
 ${promptTab('dictation','settings.ui.prompt.dictation',true)}${promptTab('dialogue','settings.ui.prompt.dialogue')}
 </div>
 ${promptEditor('dictation','settings.ui.prompt.dictation.title',true)}${promptEditor('dialogue','settings.ui.prompt.dialogue.title')}
 </section>
 <section id="panel-network" class="settings-panel" role="tabpanel" aria-labelledby="tab-network" data-settings-panel="network" hidden>
 <section class="settings-group"><h2>${text('settings.ui.search')}</h2><label class="check" for="exa-enabled"><input type="checkbox" id="exa-enabled">${text('settings.ui.search.enabled')}</label><div class="form-grid">
 <label for="search-type">${text('settings.ui.search.type')}<select id="search-type"><option value="instant">instant</option><option value="fast">fast</option></select></label><label for="search-timeout">${text('settings.ui.search.timeout')}<input id="search-timeout" type="number" min="8" max="15" required></label></div>${credential('exa','Exa')}</section>
 <section class="settings-group"><h2>${text('settings.ui.budget')}</h2><div class="form-grid">
 <label for="max-calls">${text('settings.ui.budget.max_calls')}<input id="max-calls" type="number" min="1" max="8" required></label>
 <label for="max-tool-rounds">${text('settings.ui.budget.max_rounds')}<input id="max-tool-rounds" type="number" min="0" max="8" required aria-describedby="budget-hint"></label>
 <label for="max-model-requests">${text('settings.ui.budget.max_requests')}<input id="max-model-requests" type="number" readonly aria-readonly="true"></label>
 <label for="llm-timeout">${text('settings.ui.budget.llm_timeout')}<input id="llm-timeout" type="number" min="5" max="180" required></label>
 <label for="task-timeout">${text('settings.ui.budget.task_timeout')}<input id="task-timeout" type="number" min="10" max="300" required></label>
 </div><p class="hint" id="budget-hint" data-i18n="settings.ui.budget.hint"></p></section>
 </section>
 </fieldset></form>
 <footer class="save-bar"><div class="save-status"><span id="summary" data-i18n="settings.ui.loading"></span><p id="save-message" role="status"></p></div><div class="save-actions"><button id="settings-close" type="button" data-i18n="settings.ui.close"></button><button id="save" type="submit" form="preferences" class="primary" disabled data-i18n="settings.ui.save"></button></div></footer>
 </main>`;
 const el = <T extends HTMLElement>(id:string) => root.querySelector<T>(`#${id}`)!;
 const input = (id:string) => el<HTMLInputElement>(id);
 const value = (id:string) => el<HTMLInputElement|HTMLSelectElement|HTMLTextAreaElement>(id).value;
 const defaults = () => promptDefaultsByLocale[locale];
 function renderStatus(id:string) {
  const item=feedback.get(id);const rendered=item?message(locale,item.text,item.identity):'';
  el(id).textContent=rendered;el(id).hidden=!rendered;
 }
 function status(id:string,fallback:string,identity?:Message) {feedback.set(id,{text:fallback,identity});renderStatus(id);}
 function localStatus(id:string,code:string) {status(id,'',{code});}
 function renderDevice(option:HTMLOptionElement) {const device=deviceNames.get(option);if(device)option.textContent=message(locale,device.name,device.name_i18n);}
 function deviceOption(device:Device) {const option=document.createElement('option');option.value=device.id;deviceNames.set(option,device);renderDevice(option);return option;}
 function promptSource(kind:PromptKind,source:'default'|'custom') {promptSources[kind]=source;el(`${kind}-prompt`).dataset.promptSource=source;}
 function refreshLanguage() {
  const scrolls=Array.from(root.querySelectorAll<HTMLElement>('.settings-body,textarea')).map(node=>({node,top:node.scrollTop,left:node.scrollLeft}));
  locale=resolveLocale(value('ui-language') as UILanguage,systemLocale);
  translate(root,locale);updateASRProtocol();
  for(const kind of services) {
   el(`key-label-${kind}`).textContent=t(locale,'settings.ui.key.label',{service:kind==='exa'?'Exa':kind.toUpperCase()});
   el(`key-status-${kind}`).textContent=t(locale,keySaved[kind]?'settings.ui.key.saved':'settings.ui.key.missing');
   for(const action of ['save','delete','test'])el(`${action==='test'?'test':`${action}-key`}-${kind}`).setAttribute('aria-label',t(locale,`settings.ui.key.${action}.aria`,{service:kind==='exa'?'Exa':kind.toUpperCase()}));
  }
  for(const kind of promptKinds)if(promptSources[kind]==='default') {
   const field=el<HTMLTextAreaElement>(`${kind}-prompt`);const next=defaults()[kind];
   if(field.value!==next){const {selectionStart,selectionEnd,selectionDirection}=field;field.value=next;field.setSelectionRange(selectionStart,selectionEnd,selectionDirection);}
  }
  for(const id of Object.keys(textFields))updateText(id);
  refreshValidation();
  for(const id of feedback.keys())renderStatus(id);
  for(const option of Array.from(el<HTMLSelectElement>('device').options))renderDevice(option);
  for(const {node,top,left} of scrolls){node.scrollTop=top;node.scrollLeft=left;}
 }
 el('ui-language').addEventListener('change',refreshLanguage);

 function activateTab(id:string,prompt=false) {
  const prefix=prompt?'prompt-':'';
  const tabs=Array.from(root.querySelectorAll<HTMLButtonElement>(prompt?'.prompt-tabs [role=tab]':'.settings-tabs [role=tab]'));
  for(const button of tabs) {
   const active=button.id===`${prefix}tab-${id}`;
   button.setAttribute('aria-selected',String(active));button.tabIndex=active?0:-1;
   el(button.getAttribute('aria-controls')!).hidden=!active;
  }
 }
 const narrowNavigation=window.matchMedia('(max-width: 640px)');
 const navigation=root.querySelector<HTMLElement>('.settings-tabs')!;
 const syncNavigation=()=>navigation.setAttribute('aria-orientation',narrowNavigation.matches?'horizontal':'vertical');
 syncNavigation();narrowNavigation.addEventListener('change',syncNavigation);
 for(const list of Array.from(root.querySelectorAll<HTMLElement>('[role=tablist]'))) {
  const tabs=Array.from(list.querySelectorAll<HTMLButtonElement>('[role=tab]'));
  for(const button of tabs) {
   const activate=()=>activateTab(button.dataset.tab!,list.classList.contains('prompt-tabs'));
   button.addEventListener('click',activate);
   button.addEventListener('keydown',event=>{
    const vertical=list.getAttribute('aria-orientation')==='vertical';
    let index=tabs.indexOf(button);
    if(event.key===(vertical?'ArrowDown':'ArrowRight'))index=(index+1)%tabs.length;
    else if(event.key===(vertical?'ArrowUp':'ArrowLeft'))index=(index+tabs.length-1)%tabs.length;
    else if(event.key==='Home')index=0;
    else if(event.key==='End')index=tabs.length-1;
    else return;
    event.preventDefault();tabs[index].click();tabs[index].focus();
   });
  }
 }
 function updateASRProtocol() {
  const mimo=value('asr-provider')==='mimo';
  input('asr-endpoint').placeholder=mimo?'https://api.xiaomimimo.com/v1/chat/completions':t(locale,'settings.ui.asr.endpoint.placeholder');
  input('asr-model').placeholder=mimo?'mimo-v2.5-asr':t(locale,'settings.ui.asr.model.placeholder');
  const language=input('asr-language');language.placeholder=mimo?'auto / zh / en':'zh';
  if(mimo){language.pattern='auto|zh|en';language.title=t(locale,'settings.ui.asr.language.title');}
  else{language.removeAttribute('pattern');language.removeAttribute('title');}
 }
 el('asr-provider').addEventListener('change',()=>{updateASRProtocol();refreshValidation();});
 const textFields:Record<string,string>={'profile-custom':'profile-count','dictation-prompt':'dictation-count','dialogue-prompt':'dialogue-count'};
 function updateText(id:string,constrain=false) {
  const field=el<HTMLTextAreaElement>(id);
  let points=Array.from(field.value);
  if(constrain&&points.length>textLimit){points=points.slice(0,textLimit);field.value=points.join('');}
  field.setCustomValidity(points.length>textLimit?t(locale,'settings.ui.text_limit',{limit:String(textLimit)}):'');
  el(textFields[id]).textContent=`${points.length} / ${textLimit}`;
 }
 for(const id of Object.keys(textFields)) {
  el(id).addEventListener('input',event=>updateText(id,!(event as InputEvent).isComposing));
  el(id).addEventListener('compositionend',()=>{updateText(id,true);syncControls();});
 }
 for(const kind of promptKinds)for(const event of ['input','compositionstart'])el(`${kind}-prompt`).addEventListener(event,()=>promptSource(kind,'custom'));
 // Browser-native validation strings follow the browser language, not the preview language.
 function refreshValidation() {
  for(const field of Array.from(el<HTMLFormElement>('preferences').elements)) {
   if(!(field instanceof HTMLInputElement||field instanceof HTMLSelectElement||field instanceof HTMLTextAreaElement)||field.id in textFields)continue;
   field.setCustomValidity('');const validity=field.validity;let code='';let args:Record<string,string>={};
   if(validity.valueMissing)code='settings.ui.validation.required';
   else if(validity.badInput)code='settings.ui.validation.number';
   else if(validity.typeMismatch)code='settings.ui.validation.url';
   else if(validity.patternMismatch)code=field.id==='asr-language'?'settings.ui.asr.language.title':'settings.ui.validation.invalid';
   else if((validity.rangeUnderflow||validity.rangeOverflow)&&field instanceof HTMLInputElement){code='settings.ui.validation.range';args={min:field.min,max:field.max};}
   else if(validity.stepMismatch)code='settings.ui.validation.step';
   if(code)field.setCustomValidity(t(locale,code,args));
  }
 }
 function updateBudget() {
  const field=input('max-tool-rounds');
  field.setCustomValidity('');input('max-model-requests').value=field.value!==''&&field.validity.valid?String(Number(field.value)+1):'';
 }
 el('max-tool-rounds').addEventListener('input',updateBudget);
 function fill(config:Config) {
  baseline=structuredClone(config);baseline.ui_language??='auto';
  baseline.general={...defaultConfig().general,...config.general};
  baseline.popup.auto_hide_seconds??=30;
  const values:Record<string,string|number> = {'auto-hide-seconds':baseline.popup.auto_hide_seconds,'ui-language':baseline.ui_language,'mode':config.mode,'hotkey':config.hotkey,'ai-hotkey':config.ai_hotkey,'max-seconds':config.recording.max_seconds,'asr-provider':config.asr.provider||'openai_compatible','asr-endpoint':config.asr.endpoint,'asr-model':config.asr.model,'asr-language':config.asr.language,'asr-timeout':config.asr.timeout_seconds,'llm-base':config.llm.base_url,'llm-model':config.llm.model,'output-format':config.llm.output_format,'llm-timeout':config.llm.timeout_seconds,'task-timeout':config.llm.task_timeout_seconds,'max-tool-rounds':config.llm.max_tool_rounds??2,'search-type':config.exa.search_type,'max-calls':config.exa.max_calls,'search-timeout':config.exa.timeout_seconds,'profile-custom':config.profile.custom_background};
  for(const kind of promptKinds) {
   const custom=config.llm[`${kind}_prompt`]??'';promptSource(kind,custom.trim()?'custom':'default');
   values[`${kind}-prompt`]=custom.trim()?custom:'';
  }
  for(const [id,v] of Object.entries(values))el<HTMLInputElement>(id).value=String(v);
  updateBudget();
  const select=el<HTMLSelectElement>('device');
  if(!Array.from(select.options).some(option=>option.value===config.recording.device_id))select.add(deviceOption({id:config.recording.device_id,name:'',name_i18n:{code:'settings.ui.device.saved'}}));
  select.value=config.recording.device_id;
  input('exa-enabled').checked=config.exa.enabled;
  input('start-with-windows').checked=baseline.general.start_with_windows;
  input('start-as-admin').checked=baseline.general.start_as_admin;
  input('auto-copy').checked=baseline.general.auto_copy;
  input('mute-while-recording').checked=baseline.general.mute_while_recording;
  refreshLanguage();
 }
 function readPrompt(kind:PromptKind) {
  const draft=value(`${kind}-prompt`);
  return promptSources[kind]==='default'||!draft.trim()?'':draft;
 }
 function read():Config {
  const c=structuredClone(baseline);
  c.ui_language=value('ui-language') as UILanguage;
  c.mode=value('mode') as Config['mode'];c.hotkey=value('hotkey').trim();c.ai_hotkey=value('ai-hotkey').trim();c.recording={device_id:value('device'),max_seconds:Number(value('max-seconds'))};
  c.asr={provider:value('asr-provider') as Config['asr']['provider'],endpoint:value('asr-endpoint').trim(),model:value('asr-model').trim(),language:value('asr-language').trim(),timeout_seconds:Number(value('asr-timeout'))};
  c.llm={base_url:value('llm-base').trim(),model:value('llm-model').trim(),output_format:value('output-format'),timeout_seconds:Number(value('llm-timeout')),task_timeout_seconds:Number(value('task-timeout')),dictation_prompt:readPrompt('dictation'),dialogue_prompt:readPrompt('dialogue'),max_tool_rounds:Number(value('max-tool-rounds'))};
  c.exa={enabled:input('exa-enabled').checked,search_type:value('search-type'),max_calls:Number(value('max-calls')),timeout_seconds:Number(value('search-timeout'))};
  c.general={start_with_windows:input('start-with-windows').checked,start_as_admin:input('start-with-windows').checked&&input('start-as-admin').checked,auto_copy:input('auto-copy').checked,mute_while_recording:input('mute-while-recording').checked};
  c.popup.auto_hide_seconds=Number(value('auto-hide-seconds'));
  c.profile={custom_background:value('profile-custom')};
  return c;
 }
 function serviceSettings(config:Config,kind:Service) {
  if(kind==='asr')return {provider:config.asr.provider||'openai_compatible',endpoint:config.asr.endpoint,model:config.asr.model,language:config.asr.language,timeout_seconds:config.asr.timeout_seconds};
  if(kind==='llm')return {base_url:config.llm.base_url,model:config.llm.model,output_format:config.llm.output_format,timeout_seconds:config.llm.timeout_seconds};
  return {search_type:config.exa.search_type,timeout_seconds:config.exa.timeout_seconds};
 }
 function needsSave(kind:Service) {
  return input(`key-${kind}`).value!==''||JSON.stringify(serviceSettings(read(),kind))!==JSON.stringify(serviceSettings(baseline,kind));
 }
 function syncControls() {
  el<HTMLFieldSetElement>('settings-fields').disabled=!loaded;
  el<HTMLButtonElement>('save').disabled=!loaded||saving;
  input('start-as-admin').disabled=!loaded||!input('start-with-windows').checked;
  if(!input('start-with-windows').checked)input('start-as-admin').checked=false;
  for(const kind of services) {
   const pending=needsSave(kind);
   for(const action of ['save-key','delete-key'])el<HTMLButtonElement>(`${action}-${kind}`).disabled=!loaded||busy.has(kind);
   const button=el<HTMLButtonElement>(`test-${kind}`);
   button.disabled=!loaded||busy.has(kind)||pending;
   status(`test-hint-${kind}`,'',loaded&&pending?{code:'settings.ui.key.needs_save'}:undefined);
  }
 }
 el('preferences').addEventListener('input',()=>{refreshValidation();syncControls();});
 el('preferences').addEventListener('change',()=>{refreshValidation();syncControls();});
 for(const kind of promptKinds)el(`reset-${kind}`).addEventListener('click',()=>{
  if(!loaded)return;
  const field=el<HTMLTextAreaElement>(`${kind}-prompt`);
  if(field.value!==defaults()[kind]&&!window.confirm(t(locale,'settings.ui.prompt.confirm')))return;
  promptSource(kind,'default');field.value=defaults()[kind];updateText(`${kind}-prompt`);syncControls();
 });
 function validate() {
  for(const id of Object.keys(textFields))updateText(id);
  refreshValidation();
  const invalid=Array.from(el<HTMLFormElement>('preferences').elements).find(field=>
   (field instanceof HTMLInputElement||field instanceof HTMLSelectElement||field instanceof HTMLTextAreaElement)&&field.willValidate&&!field.checkValidity()
  ) as HTMLInputElement|HTMLSelectElement|HTMLTextAreaElement|undefined;
  if(!invalid)return true;
  const panel=invalid.closest<HTMLElement>('[data-settings-panel]');
  if(panel)activateTab(panel.dataset.settingsPanel!);
  const promptPanel=invalid.closest<HTMLElement>('[data-prompt-panel]');
  if(promptPanel)activateTab(promptPanel.dataset.promptPanel!,true);
  invalid.focus();invalid.reportValidity();return false;
 }
 async function perform(method:string,args:unknown[],messageID:string) {
  if(preview){localStatus('bridge-error','settings.ui.preview');return null;}
  try{const result=await call<ActionResult>(method,...args);status(messageID,result.message,result.message_i18n);return result;}
  catch{localStatus(messageID,'settings.ui.operation_failed');return null;}
 }
 el('preferences').addEventListener('submit',async event=>{
  event.preventDefault();
  if(!loaded||saving||!validate())return;
  const config=read();saving=true;syncControls();
  try {
   const result=await perform('SaveSettings',[config],'save-message');
   if(result?.saved||result?.ok)baseline=structuredClone(config);
  }finally{saving=false;syncControls();}
 });
 for(const kind of services) {
  el(`save-key-${kind}`).addEventListener('click',async()=>{
   if(!loaded||busy.has(kind))return;
   const field=input(`key-${kind}`);const key=field.value;field.value='';busy.add(kind);syncControls();
   try{const result=await perform('SaveKey',[kind,key],`key-message-${kind}`);if(result?.ok){keySaved[kind]=true;el(`key-status-${kind}`).textContent=t(locale,'settings.ui.key.saved');}}
   finally{busy.delete(kind);syncControls();}
  });
  el(`delete-key-${kind}`).addEventListener('click',async()=>{
   if(!loaded||busy.has(kind))return;
   busy.add(kind);syncControls();
   try{const result=await perform('DeleteKey',[kind],`key-message-${kind}`);if(result?.ok){keySaved[kind]=false;el(`key-status-${kind}`).textContent=t(locale,'settings.ui.key.missing');}}
   finally{busy.delete(kind);syncControls();}
  });
  el(`test-${kind}`).addEventListener('click',async()=>{
   if(!loaded||busy.has(kind)||needsSave(kind))return;
   busy.add(kind);syncControls();if(!preview)localStatus(`key-message-${kind}`,'settings.ui.key.testing');
   try{await perform('TestConnection',[kind],`key-message-${kind}`);}
   finally{busy.delete(kind);syncControls();}
  });
 }
 el('refresh-devices').addEventListener('click',async()=>{
  if(!loaded)return;
  if(preview){localStatus('device-message','settings.ui.preview');return;}
  const button=el<HTMLButtonElement>('refresh-devices');button.disabled=true;
  try{const result=await call<{devices:Device[];message:string;message_i18n?:Message}>('ListDevices');const select=el<HTMLSelectElement>('device');const selected=select.value;select.replaceChildren(...(result.devices??[]).map(deviceOption));
   if(!Array.from(select.options).some(option=>option.value===selected))select.add(deviceOption({id:selected,name:'',name_i18n:{code:'settings.ui.device.unavailable'}}));select.value=selected;
   if(result.message||result.message_i18n)status('device-message',result.message,result.message_i18n);else localStatus('device-message','settings.ui.devices.refreshed');
  }catch{localStatus('device-message','settings.ui.devices.failed');}finally{button.disabled=false;}
 });
 el('settings-close').addEventListener('click',async()=>{if(preview){localStatus('bridge-error','settings.ui.preview');return;}try{await call('CloseSettings');}catch{localStatus('save-message','settings.ui.close_failed');}});
 fill(baseline);syncControls();
 if(preview){loaded=true;syncControls();localStatus('bridge-error','settings.ui.preview');}
 else {
  call<SettingsView>('GetSettings').then(view=>{
   systemLocale=view.system_locale??view.locale??browserLocale();
   promptDefaultsByLocale=view.prompt_defaults_by_locale??{en:view.prompt_defaults,'zh-CN':view.prompt_defaults};
   for(const kind of services)keySaved[kind]=Boolean(view.keys[kind]);
   fill(view.config);loaded=true;
   el('summary').removeAttribute('data-i18n');el('summary').textContent=`v${view.version}`;
   if(view.credential_message||view.credential_message_i18n)status('bridge-error',view.credential_message??'',view.credential_message_i18n);
   syncControls();
  }).catch(()=>localStatus('bridge-error','settings.ui.load_failed'));
  const refreshError=()=>call<string|{message:string;message_i18n?:Message}>('StartupError').then(error=>typeof error==='string'?status('startup-error',error):status('startup-error',error.message,error.message_i18n)).catch(()=>{});
  void refreshError();setInterval(()=>void refreshError(),1500);
 }
}
function tab(id:string,label:string,active=false) {
 return `<button id="tab-${id}" type="button" role="tab" aria-controls="panel-${id}" aria-selected="${active}" tabindex="${active?0:-1}" data-tab="${id}" data-i18n="${label}"></button>`;
}
function promptTab(kind:PromptKind,label:string,active=false) {
 return `<button id="prompt-tab-${kind}" type="button" role="tab" aria-controls="prompt-panel-${kind}" aria-selected="${active}" tabindex="${active?0:-1}" data-tab="${kind}" data-i18n="${label}"></button>`;
}
function promptEditor(kind:PromptKind,label:string,active=false) {
 return `<section id="prompt-panel-${kind}" class="prompt-editor" role="tabpanel" aria-labelledby="prompt-tab-${kind}" data-prompt-panel="${kind}" ${active?'':'hidden'}><div class="editor-heading"><h2><label for="${kind}-prompt">${text(label)}</label></h2><button id="reset-${kind}" type="button" data-i18n="settings.ui.prompt.restore"></button></div><textarea id="${kind}-prompt" class="prompt-input" rows="15" aria-describedby="${kind}-count" data-i18n-placeholder="settings.ui.prompt.placeholder"></textarea><output id="${kind}-count" class="character-count" for="${kind}-prompt"></output></section>`;
}
function credential(kind:Service,label:string):string {
 return `<div class="credential"><label for="key-${kind}"><span id="key-label-${kind}">${label}</span> <span id="key-status-${kind}" class="key-status"></span></label><input id="key-${kind}" type="password" maxlength="2560" autocomplete="new-password" spellcheck="false" data-i18n-placeholder="settings.ui.key.placeholder"><div class="credential-actions"><button type="button" id="save-key-${kind}" data-i18n="settings.ui.key.save"></button><button type="button" id="delete-key-${kind}" data-i18n="settings.ui.key.delete"></button><button type="button" id="test-${kind}" aria-describedby="test-hint-${kind}" data-i18n="settings.ui.key.test"></button></div><p id="test-hint-${kind}" class="hint" hidden></p><p id="key-message-${kind}" class="hint" role="status" hidden></p></div>`;
}
