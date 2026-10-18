import {APP_NAME, call, preview, defaultConfig, type Config, type SettingsView, type ActionResult} from './api';

type Device = {id:string;name:string};
type Service = 'asr'|'llm'|'exa';
type PromptKind = 'dictation'|'dialogue';
const services:Service[] = ['asr','llm','exa'];
const textLimit = 16000;

export function mountSettings(root:HTMLElement) {
 let baseline = defaultConfig();
 let promptDefaults = {dictation:'',dialogue:''};
 let loaded = false;
 let saving = false;
 const busy = new Set<Service>();
 root.innerHTML = `<main class="settings">
 <header class="settings-header"><img class="mark" src="/app-icon.png" width="28" height="28" alt="" aria-hidden="true"><h1>${APP_NAME}</h1></header>
 <nav class="settings-tabs" role="tablist" aria-label="设置分类" aria-orientation="vertical">
 ${tab('input','输入',true)}${tab('models','模型服务')}${tab('prompts','提示词')}${tab('network','联网与预算')}
 </nav>
 <form id="preferences" class="settings-body" novalidate>
 <p class="warning" id="bridge-error" role="alert" hidden></p><p class="warning" id="startup-error" role="alert" hidden></p>
 <fieldset id="settings-fields" aria-label="设置内容" disabled>
 <section id="panel-input" class="settings-panel" role="tabpanel" aria-labelledby="tab-input" data-settings-panel="input">
 <section class="settings-group"><h2>输入与录音</h2><div class="form-grid">
 <label class="span-two" for="mode">工作模式<select id="mode"><option value="live">ASR + LLM</option><option value="asr_mock_llm">ASR 直通</option></select></label>
 <label for="hotkey">语音润色快捷键<input id="hotkey" required maxlength="80" placeholder="Ctrl+Alt+R"></label>
 <label for="ai-hotkey">AI 对话快捷键<input id="ai-hotkey" required maxlength="80" placeholder="Ctrl+Alt+A"></label>
 <label for="max-seconds">最长录音（秒）<input id="max-seconds" type="number" min="1" max="300" required></label>
 <div class="span-two"><label for="device">麦克风</label><div class="inline-field"><select id="device"><option value="default">系统默认麦克风</option></select><button id="refresh-devices" type="button">刷新设备</button></div><small id="device-message" role="status" hidden></small></div>
 </div></section>
 <section class="settings-group"><h2><label for="profile-custom">背景信息</label></h2><textarea id="profile-custom" class="background-input" rows="8" aria-describedby="profile-count" placeholder="术语、称呼、表达偏好……"></textarea><output id="profile-count" class="character-count" for="profile-custom"></output></section>
 </section>
 <section id="panel-models" class="settings-panel" role="tabpanel" aria-labelledby="tab-models" data-settings-panel="models" hidden>
 <section class="settings-group"><h2>语音识别 ASR</h2><div class="form-grid">
 <label class="span-two" for="asr-provider">ASR 协议<select id="asr-provider"><option value="openai_compatible">OpenAI-compatible</option><option value="mimo">MiMo ASR</option></select></label>
 <label class="span-two" for="asr-endpoint">完整 endpoint<input id="asr-endpoint" type="url" placeholder="https://服务地址/v1/audio/transcriptions"></label>
 <label for="asr-model">模型<input id="asr-model" maxlength="200" autocomplete="off"></label><label for="asr-language">语言<input id="asr-language" maxlength="20" placeholder="zh"></label>
 <label for="asr-timeout">超时（秒）<input id="asr-timeout" type="number" min="5" max="180" required></label></div>${credential('asr','ASR')}</section>
 <section class="settings-group"><h2>语义模型 LLM</h2><div class="form-grid">
 <label class="span-two" for="llm-base">Base URL<input id="llm-base" type="url" placeholder="https://服务地址/v1"></label><label for="llm-model">模型<input id="llm-model" maxlength="200" autocomplete="off"></label>
 <label for="output-format">结构化输出<select id="output-format"><option value="json_schema">json_schema</option><option value="json_object">json_object</option></select></label>
 </div>${credential('llm','LLM')}</section>
 </section>
 <section id="panel-prompts" class="settings-panel" role="tabpanel" aria-labelledby="tab-prompts" data-settings-panel="prompts" hidden>
 <div class="prompt-tabs" role="tablist" aria-label="提示词用途" aria-orientation="horizontal">
 ${promptTab('dictation','语音润色',true)}${promptTab('dialogue','AI 对话')}
 </div>
 ${promptEditor('dictation','语音润色',true)}${promptEditor('dialogue','AI 对话')}
 </section>
 <section id="panel-network" class="settings-panel" role="tabpanel" aria-labelledby="tab-network" data-settings-panel="network" hidden>
 <section class="settings-group"><h2>联网搜索 Exa</h2><label class="check" for="exa-enabled"><input type="checkbox" id="exa-enabled">启用联网搜索</label><div class="form-grid">
 <label for="search-type">搜索类型<select id="search-type"><option value="instant">instant</option><option value="fast">fast</option></select></label><label for="search-timeout">搜索超时（秒）<input id="search-timeout" type="number" min="8" max="15" required></label></div>${credential('exa','Exa')}</section>
 <section class="settings-group"><h2>每轮处理预算</h2><div class="form-grid">
 <label for="max-calls">最大搜索次数<input id="max-calls" type="number" min="1" max="8" required></label>
 <label for="max-tool-rounds">最大工具轮数<input id="max-tool-rounds" type="number" min="0" max="8" required aria-describedby="budget-hint"></label>
 <label for="max-model-requests">模型请求上限<input id="max-model-requests" type="number" readonly aria-readonly="true"></label>
 <label for="llm-timeout">LLM 单次超时（秒）<input id="llm-timeout" type="number" min="5" max="180" required></label>
 <label for="task-timeout">每轮处理超时（秒）<input id="task-timeout" type="number" min="10" max="300" required></label>
 </div><p class="hint" id="budget-hint">工具轮数为 T，模型请求上限为 T+1；T=0 时不使用工具。</p></section>
 </section>
 </fieldset></form>
 <footer class="save-bar"><div class="save-status"><span id="summary">加载设置…</span><p id="save-message" role="status"></p></div><div class="save-actions"><button id="settings-close" type="button">关闭设置</button><button id="save" type="submit" form="preferences" class="primary" disabled>保存设置</button></div></footer>
 </main>`;
 const el = <T extends HTMLElement>(id:string) => root.querySelector<T>(`#${id}`)!;
 const input = (id:string) => el<HTMLInputElement>(id);
 const value = (id:string) => el<HTMLInputElement|HTMLSelectElement|HTMLTextAreaElement>(id).value;
 function status(id:string,message:string) {el(id).textContent=message;el(id).hidden=!message;}
 function deviceOption(device:Device) {const option=document.createElement('option');option.value=device.id;option.textContent=device.name;return option;}

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
  input('asr-endpoint').placeholder=mimo?'https://api.xiaomimimo.com/v1/chat/completions':'https://服务地址/v1/audio/transcriptions';
  input('asr-model').placeholder=mimo?'mimo-v2.5-asr':'服务支持的转录模型';
  const language=input('asr-language');language.placeholder=mimo?'auto / zh / en':'zh';
  if(mimo){language.pattern='auto|zh|en';language.title='留空、auto、zh 或 en';}
  else{language.removeAttribute('pattern');language.removeAttribute('title');}
 }
 el('asr-provider').addEventListener('change',updateASRProtocol);
 const textFields:Record<string,string>={'profile-custom':'profile-count','dictation-prompt':'dictation-count','dialogue-prompt':'dialogue-count'};
 function updateText(id:string,constrain=false) {
  const field=el<HTMLTextAreaElement>(id);
  let points=Array.from(field.value);
  if(constrain&&points.length>textLimit){points=points.slice(0,textLimit);field.value=points.join('');}
  field.setCustomValidity(points.length>textLimit?'最多 16000 个字符':'');
  el(textFields[id]).textContent=`${points.length} / ${textLimit}`;
 }
 for(const id of Object.keys(textFields)) {
  el(id).addEventListener('input',event=>updateText(id,!(event as InputEvent).isComposing));
  el(id).addEventListener('compositionend',()=>{updateText(id,true);syncControls();});
 }
 function updateBudget() {
  const field=input('max-tool-rounds');
  input('max-model-requests').value=field.value!==''&&field.validity.valid?String(Number(field.value)+1):'';
 }
 el('max-tool-rounds').addEventListener('input',updateBudget);
 function fill(config:Config) {
  baseline=structuredClone(config);
  const values:Record<string,string|number> = {'mode':config.mode,'hotkey':config.hotkey,'ai-hotkey':config.ai_hotkey,'max-seconds':config.recording.max_seconds,'asr-provider':config.asr.provider||'openai_compatible','asr-endpoint':config.asr.endpoint,'asr-model':config.asr.model,'asr-language':config.asr.language,'asr-timeout':config.asr.timeout_seconds,'llm-base':config.llm.base_url,'llm-model':config.llm.model,'output-format':config.llm.output_format,'llm-timeout':config.llm.timeout_seconds,'task-timeout':config.llm.task_timeout_seconds,'max-tool-rounds':config.llm.max_tool_rounds??2,'search-type':config.exa.search_type,'max-calls':config.exa.max_calls,'search-timeout':config.exa.timeout_seconds,'profile-custom':config.profile.custom_background};
  for(const kind of ['dictation','dialogue'] as const) {
   const custom=config.llm[`${kind}_prompt`]??'';
   values[`${kind}-prompt`]=custom.trim()?custom:promptDefaults[kind];
  }
  for(const [id,v] of Object.entries(values))el<HTMLInputElement>(id).value=String(v);
  updateASRProtocol();updateBudget();
  for(const id of Object.keys(textFields))updateText(id);
  const select=el<HTMLSelectElement>('device');
  if(!Array.from(select.options).some(option=>option.value===config.recording.device_id))select.add(deviceOption({id:config.recording.device_id,name:'已保存的麦克风'}));
  select.value=config.recording.device_id;
  input('exa-enabled').checked=config.exa.enabled;
 }
 function readPrompt(kind:PromptKind) {
  const draft=value(`${kind}-prompt`);
  return !draft.trim()||draft===promptDefaults[kind]?'':draft;
 }
 function read():Config {
  const c=structuredClone(baseline);
  c.mode=value('mode') as Config['mode'];c.hotkey=value('hotkey').trim();c.ai_hotkey=value('ai-hotkey').trim();c.recording={device_id:value('device'),max_seconds:Number(value('max-seconds'))};
  c.asr={provider:value('asr-provider') as Config['asr']['provider'],endpoint:value('asr-endpoint').trim(),model:value('asr-model').trim(),language:value('asr-language').trim(),timeout_seconds:Number(value('asr-timeout'))};
  c.llm={base_url:value('llm-base').trim(),model:value('llm-model').trim(),output_format:value('output-format'),timeout_seconds:Number(value('llm-timeout')),task_timeout_seconds:Number(value('task-timeout')),dictation_prompt:readPrompt('dictation'),dialogue_prompt:readPrompt('dialogue'),max_tool_rounds:Number(value('max-tool-rounds'))};
  c.exa={enabled:input('exa-enabled').checked,search_type:value('search-type'),max_calls:Number(value('max-calls')),timeout_seconds:Number(value('search-timeout'))};
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
  for(const kind of services) {
   const pending=needsSave(kind);
   for(const action of ['save-key','delete-key'])el<HTMLButtonElement>(`${action}-${kind}`).disabled=!loaded||busy.has(kind);
   const button=el<HTMLButtonElement>(`test-${kind}`);
   button.disabled=!loaded||busy.has(kind)||pending;
   status(`test-hint-${kind}`,loaded&&pending?'先保存设置或密钥':'');
  }
 }
 el('preferences').addEventListener('input',syncControls);
 el('preferences').addEventListener('change',syncControls);
 for(const kind of ['dictation','dialogue'] as const)el(`reset-${kind}`).addEventListener('click',()=>{
  if(!loaded)return;
  const field=el<HTMLTextAreaElement>(`${kind}-prompt`);
  if(field.value!==promptDefaults[kind]&&!window.confirm('恢复默认提示词？当前修改将被替换。'))return;
  field.value=promptDefaults[kind];updateText(`${kind}-prompt`);syncControls();
 });
 function validate() {
  for(const id of Object.keys(textFields))updateText(id);
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
  if(preview){status('bridge-error','只读预览');return null;}
  try{const result=await call<ActionResult>(method,...args);status(messageID,result.message);return result;}
  catch{status(messageID,'操作失败，请重试');return null;}
 }
 el('preferences').addEventListener('submit',async event=>{
  event.preventDefault();
  if(!loaded||saving||!validate())return;
  const config=read();saving=true;syncControls();
  try {
   const result=await perform('SaveSettings',[config],'save-message');
   if(result?.ok)baseline=structuredClone(config);
  }finally{saving=false;syncControls();}
 });
 for(const kind of services) {
  el(`save-key-${kind}`).addEventListener('click',async()=>{
   if(!loaded||busy.has(kind))return;
   const field=input(`key-${kind}`);const key=field.value;field.value='';busy.add(kind);syncControls();
   try{const result=await perform('SaveKey',[kind,key],`key-message-${kind}`);if(result?.ok)el(`key-status-${kind}`).textContent='已保存';}
   finally{busy.delete(kind);syncControls();}
  });
  el(`delete-key-${kind}`).addEventListener('click',async()=>{
   if(!loaded||busy.has(kind))return;
   busy.add(kind);syncControls();
   try{const result=await perform('DeleteKey',[kind],`key-message-${kind}`);if(result?.ok)el(`key-status-${kind}`).textContent='未配置';}
   finally{busy.delete(kind);syncControls();}
  });
  el(`test-${kind}`).addEventListener('click',async()=>{
   if(!loaded||busy.has(kind)||needsSave(kind))return;
   busy.add(kind);syncControls();if(!preview)status(`key-message-${kind}`,'测试中……');
   try{await perform('TestConnection',[kind],`key-message-${kind}`);}
   finally{busy.delete(kind);syncControls();}
  });
 }
 el('refresh-devices').addEventListener('click',async()=>{
  if(!loaded)return;
  if(preview){status('device-message','只读预览');return;}
  const button=el<HTMLButtonElement>('refresh-devices');button.disabled=true;
  try{const result=await call<{devices:Device[];message:string}>('ListDevices');const select=el<HTMLSelectElement>('device');const selected=select.value;select.replaceChildren(...(result.devices??[]).map(deviceOption));
   if(!Array.from(select.options).some(option=>option.value===selected))select.add(deviceOption({id:selected,name:'原设备（不可用）'}));select.value=selected;status('device-message',result.message||'已刷新');
  }catch{status('device-message','设备枚举失败，请检查权限');}finally{button.disabled=false;}
 });
 el('settings-close').addEventListener('click',async()=>{if(preview){status('bridge-error','只读预览');return;}try{await call('CloseSettings');}catch{status('save-message','关闭失败，请重试');}});
 fill(baseline);syncControls();
 if(preview){loaded=true;syncControls();status('bridge-error','只读预览');}
 else {
  call<SettingsView>('GetSettings').then(view=>{
   promptDefaults=view.prompt_defaults;fill(view.config);loaded=true;
   el('summary').textContent=`v${view.version}`;
   for(const kind of services)el(`key-status-${kind}`).textContent=view.keys[kind]?'已保存':'未配置';
   if(view.credential_message)status('bridge-error',view.credential_message);
   syncControls();
  }).catch(()=>status('bridge-error','无法读取设置，已禁用保存'));
  const refreshError=()=>call<string>('StartupError').then(error=>status('startup-error',error)).catch(()=>{});
  void refreshError();setInterval(()=>void refreshError(),1500);
 }
}
function tab(id:string,label:string,active=false) {
 return `<button id="tab-${id}" type="button" role="tab" aria-controls="panel-${id}" aria-selected="${active}" tabindex="${active?0:-1}" data-tab="${id}">${label}</button>`;
}
function promptTab(kind:PromptKind,label:string,active=false) {
 return `<button id="prompt-tab-${kind}" type="button" role="tab" aria-controls="prompt-panel-${kind}" aria-selected="${active}" tabindex="${active?0:-1}" data-tab="${kind}">${label}</button>`;
}
function promptEditor(kind:PromptKind,label:string,active=false) {
 return `<section id="prompt-panel-${kind}" class="prompt-editor" role="tabpanel" aria-labelledby="prompt-tab-${kind}" data-prompt-panel="${kind}" ${active?'':'hidden'}><div class="editor-heading"><h2><label for="${kind}-prompt">${label}提示词</label></h2><button id="reset-${kind}" type="button">恢复默认</button></div><textarea id="${kind}-prompt" class="prompt-input" rows="15" aria-describedby="${kind}-count" placeholder="内置默认"></textarea><output id="${kind}-count" class="character-count" for="${kind}-prompt"></output></section>`;
}
function credential(kind:Service,label:string):string {
 return `<div class="credential"><label for="key-${kind}">${label} 密钥 <span id="key-status-${kind}" class="key-status">未配置</span></label><input id="key-${kind}" type="password" maxlength="2560" autocomplete="new-password" spellcheck="false" placeholder="输入新密钥"><div class="credential-actions"><button type="button" id="save-key-${kind}" aria-label="保存 ${label} 密钥">保存密钥</button><button type="button" id="delete-key-${kind}" aria-label="删除 ${label} 密钥">删除密钥</button><button type="button" id="test-${kind}" aria-label="测试 ${label} 连接" aria-describedby="test-hint-${kind}">测试连接</button></div><p id="test-hint-${kind}" class="hint" hidden></p><p id="key-message-${kind}" class="hint" role="status" hidden></p></div>`;
}
