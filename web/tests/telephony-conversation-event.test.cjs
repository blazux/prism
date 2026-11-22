const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm'),path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../app.js'),'utf8');
function harness(){
 const messages=[],streamNode={fixture:'active stream'},host={querySelectorAll:()=>messages,append:node=>messages.push(node)};
 const ctx={currentSessionID:'assistant',conversationEventIDs:new Set(),document:{getElementById:()=>host,createElement:()=>({dataset:{},innerHTML:''})},
   chatWasAtBottom:()=>false,scrollChat:()=>{throw Error('Unexpected scroll')},prepareChatImages:()=>{},agentChip:()=>'',escHtml:String,AGENT_NAME:'Fixture',renderMarkdown:s=>s,fmtTime:()=> '12:00',Date,
   currentAssistantEl:streamNode,currentAssistantContent:'Current partial answer',isStreaming:true};
 vm.createContext(ctx);vm.runInContext(source.slice(source.indexOf('function receiveConversationEvent('),source.indexOf('function receiveChatReceipt(')),ctx);
 return {ctx,messages,streamNode};
}
test('background conversation result is scoped, static and deduplicated without disturbing a live answer',()=>{
 const {ctx,messages,streamNode}=harness(),event={id:42,session:'assistant',role:'assistant',content:'Call result'};
 ctx.receiveConversationEvent({...event,session:'other'});assert.equal(messages.length,0);
 ctx.receiveConversationEvent(event);ctx.receiveConversationEvent(event);assert.equal(messages.length,1);
 assert.equal(messages[0].dataset.conversationId,'42');assert.ok(messages[0].innerHTML.includes('Call result'));
 assert.equal(ctx.currentAssistantEl,streamNode);assert.equal(ctx.currentAssistantContent,'Current partial answer');assert.equal(ctx.isStreaming,true);
});
test('a result already restored from history is not appended twice',()=>{
 const {ctx,messages}=harness();messages.push({dataset:{conversationId:'42'}});
 ctx.receiveConversationEvent({id:42,session:'assistant',role:'assistant',content:'Call result'});assert.equal(messages.length,1);
});
