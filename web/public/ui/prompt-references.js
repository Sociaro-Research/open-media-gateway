/* Playground adapter for the shared native-textarea reference editor. */
(() => {

 const input=document.querySelector('#prompt');if(!input)return;
 window.PromptReferences=PromptReferenceEditor.attach(input,{id:'promptReference',model:()=>selectedModel(),options:()=>selectedModel()?requestOptions(selectedModel()):{},provider:()=>selectedProvider(),events:[[window,'gateway-reference-change'],[document.querySelector('#referenceMode'),'change'],...['imageURLs','videoURL','audioURL'].map(id=>[document.getElementById(id),'input'])]});
})();
