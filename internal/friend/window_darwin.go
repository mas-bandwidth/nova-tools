//go:build darwin

package friend

import (
	"context"
	"errors"
	"strings"
)

// WindowReach drives only an explicitly named app bundle. The JXA program asks
// AXIsProcessTrusted without prompting, requires exactly one enabled empty
// composer and one enabled send control, then writes AXValue and AXPresses it.
// It never sends keys to a terminal or guesses a focused window.
func WindowReach(ctx context.Context, run Exec, bundle, window, composer, text string) error {
	if strings.TrimSpace(bundle) == "" || strings.TrimSpace(window) == "" || strings.TrimSpace(composer) == "" {
		return errors.New("window reach needs --window-bundle, --window-title and --composer-id to identify one idle composer")
	}
	if run == nil {
		return errors.New("window reach has no harness executor")
	}
	out, exit, err := run(ctx, "", "osascript", []string{"-l", "JavaScript", "-e", windowJXA, bundle, window, composer, text}, "")
	if err != nil || exit != 0 {
		return errors.New("window reach could not inspect the configured app: " + strings.TrimSpace(out))
	}
	if strings.TrimSpace(out) != "OK" {
		return errors.New("window reach refused: " + strings.TrimSpace(out))
	}
	return nil
}

const windowJXA = `ObjC.import('ApplicationServices'); ObjC.import('AppKit');
function run(argv) {
 const bundle=argv[0], wantedWindow=argv[1], wantedComposer=argv[2], text=argv[3];
 if (!$.AXIsProcessTrusted()) { return 'Accessibility permission is not granted; grant it in Privacy & Security to nova-friend, then retry'; }
 const apps=$.NSRunningApplication.runningApplicationsWithBundleIdentifier($(bundle)); if (apps.count()!==1) return 'configured bundle is not one running app';
 function attr(e,n){const r=Ref(); return $.AXUIElementCopyAttributeValue(e,$(n),r)===0?r[0]:null;}
 function s(x){return x===null?'':String(ObjC.unwrap(x));}
 const app=$.AXUIElementCreateApplication(apps.objectAtIndex(0).processIdentifier), wins=attr(app,'AXWindows'); if(!wins) return 'configured app has no accessible windows';
 let chosen=[]; for(let i=0;i<wins.count();i++){let w=wins.objectAtIndex(i);if(s(attr(w,'AXTitle'))===wantedWindow)chosen.push(w);} if(chosen.length!==1)return 'configured window is missing or ambiguous';
 let nodes=[chosen[0]], seen=0, edit=[], send=[]; while(nodes.length){let n=nodes.pop();if(++seen>256)return 'configured window has too many accessibility nodes'; let id=s(attr(n,'AXIdentifier')), role=s(attr(n,'AXRole')); if((role==='AXTextArea'||role==='AXTextField')&&id===wantedComposer)edit.push(n); if(role==='AXButton')send.push(n); let kids=attr(n,'AXChildren');if(kids)for(let i=0;i<kids.count();i++)nodes.push(kids.objectAtIndex(i));}
 if(edit.length!==1)return 'configured composer is missing or ambiguous'; let e=edit[0]; if(!ObjC.unwrap(attr(e,'AXEnabled'))||s(attr(e,'AXValue'))!=='')return 'configured composer is busy or nonempty';
 let near=send.filter(x=>ObjC.unwrap(attr(x,'AXEnabled'))&&/send|submit/i.test(s(attr(x,'AXTitle'))+' '+s(attr(x,'AXDescription')))); if(near.length!==1)return 'send control is missing or ambiguous';
 if($.AXUIElementSetAttributeValue(e,$('AXValue'),$(text))!==0)return 'configured composer refused the message'; if($.AXUIElementPerformAction(near[0],$('AXPress'))!==0)return 'configured send control refused the message'; return 'OK';
}`
