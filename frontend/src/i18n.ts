import messages from '../../internal/i18n/messages.json';
import popup from '../../internal/i18n/popup.json';
import settings from '../../internal/i18n/settings.json';
import type {Locale, Message, UILanguage} from './api';

type Dictionary = Record<Locale,Record<string,string>>;
const dictionary:Dictionary = {
 en:{...messages.en,...popup.en,...settings.en},
 'zh-CN':{...messages['zh-CN'],...popup['zh-CN'],...settings['zh-CN']},
};
export const browserLocale = ():Locale => navigator.language.toLowerCase().startsWith('zh')?'zh-CN':'en';
export const resolveLocale = (language:UILanguage,systemLocale:Locale):Locale => language==='auto'?systemLocale:language;
const template = (locale:Locale,code:string):string|undefined => Object.hasOwn(dictionary[locale],code)?dictionary[locale][code]:undefined;
export function t(locale:Locale,code:string,args:Record<string,string>={}) {
 const value=template(locale,code)??template('en',code)??code;
 return value.replace(/\{([^{}]+)\}/g,(placeholder,name:string)=>Object.hasOwn(args,name)?args[name]:placeholder);
}
export function message(locale:Locale,fallback:string,identity?:Message) {
 if(!identity)return fallback;
 return template(locale,identity.code)!==undefined?t(locale,identity.code,identity.args):t(locale,'settings.ui.operation_failed');
}
// Only dedicated text nodes/attributes are translated; controls and user content stay intact.
export function translate(root:HTMLElement,locale:Locale) {
 document.documentElement.lang=locale;
 for(const node of Array.from(root.querySelectorAll<HTMLElement>('[data-i18n]')))node.textContent=t(locale,node.dataset.i18n!);
 for(const attribute of ['aria-label','title','placeholder'] as const) {
  for(const node of Array.from(root.querySelectorAll<HTMLElement>(`[data-i18n-${attribute}]`)))node.setAttribute(attribute,t(locale,node.getAttribute(`data-i18n-${attribute}`)!));
 }
}
