import { Call } from '@wailsio/runtime';
export const APP_NAME = '真实语义输入';
export const preview = !location.protocol.startsWith('wails') && ['localhost','127.0.0.1'].includes(location.hostname);
export const call = <T>(method: string, ...args: unknown[]): Promise<T> => Call.ByName(`main.Desktop.${method}`, ...args);
export type ActionResult = {ok:boolean;message:string};
export type Config = {
 version:number;mode:'asr_mock_llm'|'live';hotkey:string;ai_hotkey:string;
 recording:{device_id:string;max_seconds:number};
 asr:{provider?:'openai_compatible'|'mimo'|'';endpoint:string;model:string;language:string;timeout_seconds:number};
 llm:{base_url:string;model:string;timeout_seconds:number;task_timeout_seconds:number;output_format:string;dictation_prompt:string;dialogue_prompt:string;max_tool_rounds:number};
 exa:{enabled:boolean;search_type:string;max_calls:number;timeout_seconds:number};
 profile:{custom_background:string};
 popup:{auto_hide_seconds:number;always_on_top:boolean};privacy:{save_history:boolean;log_text:boolean};
};
export type SettingsView = {config:Config;keys:Record<string,boolean>;version:string;prompt_defaults:{dictation:string;dialogue:string};credential_message?:string};
export const defaultConfig = ():Config => ({version:1,mode:'live',hotkey:'Ctrl+Alt+R',ai_hotkey:'Ctrl+Alt+A',recording:{device_id:'default',max_seconds:120},asr:{provider:'openai_compatible',endpoint:'',model:'',language:'',timeout_seconds:45},llm:{base_url:'',model:'',timeout_seconds:45,task_timeout_seconds:90,output_format:'json_schema',dictation_prompt:'',dialogue_prompt:'',max_tool_rounds:2},exa:{enabled:false,search_type:'instant',max_calls:2,timeout_seconds:10},profile:{custom_background:''},popup:{auto_hide_seconds:30,always_on_top:true},privacy:{save_history:false,log_text:false}});
