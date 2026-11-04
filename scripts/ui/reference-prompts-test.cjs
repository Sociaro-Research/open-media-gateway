const assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),r=require('../../web/public/ui/reference-prompts.js');
const fixtures=JSON.parse(fs.readFileSync(path.join(__dirname,'../../internal/templates/testdata/ui-reference-grammar.json'))),typed={supports_image_input:true,supports_video_input:true,supports_audio_input:true},options={image_urls:['i1','i2'],video_urls:['v1'],audio_urls:['a1'],reference_mode:'references'},before=JSON.stringify(options);
for(const f of fixtures){const actual=r.markers(f.prompt);assert.deepEqual(actual.map(m=>m.kind),f.kinds,f.prompt);assert.equal(actual.some(m=>!m.valid),!!f.invalid,f.prompt);}
for(const syntax of ['numbered','picture','bracket','kling-id',undefined]){
 const m={...typed,reference_prompt_syntax:syntax,reference_prompt_recommendation:{text:'Use other words',kind:'instruction'}};
 assert.equal(r.error('@Picture_1 [Video 1] {Audio1}',m,options),'');assert.match(r.error('@Image3',m,options),/@Image3/);assert.equal(r.inspect('Image 99 or @Image 99',m,options).length,0);
}
assert.match(r.error('@Audio1',{supports_image_input:true},options),/Unsupported reference type/);
assert.equal(r.error('@Image1',{...typed,no_prompt:true},options),'');
const frameModel={...typed,reference_modes:[{id:'first_frame',max_images:1,max_videos:0,max_audios:0}]};assert.match(r.error('@Audio1',frameModel,{...options,reference_mode:'first_frame'}),/Unsupported/);
assert.equal(r.error('@Image1',typed,{image_urls:['first','ref'],reference_mode:'first_frame_references'}),'');assert.match(r.error('@Image2',typed,{image_urls:['first','ref'],reference_mode:'first_frame_references'}),/@Image2/);
assert.equal(r.error('@Image3 @Image3 @Video2 @Audio2',typed,options),'Missing references: @Image3, @Video2, @Audio2.');
assert.match(r.error('@Image0 @Image3',typed,options),/Invalid reference/);
assert.match(r.error('@Video1 @Image3',{supports_image_input:true},options),/Unsupported reference type/);
assert.equal(r.error('Image 3 @Image 3',typed,options),'');
assert.equal(r.error('@Image1',{...typed,no_prompt:true},{}),'');
assert.equal(r.error('@Image1',typed,{image_urls:['first'],reference_mode:'first_frame_references'}),'Missing image references: @Image1.');
const mixed=r.warning('@Image0 @Image2 @Audio1',{supports_image_input:true},{image_urls:['one']});assert.match(mixed,/Invalid reference: @Image0/);assert.match(mixed,/Unsupported reference type: @Audio1/);assert.match(mixed,/Missing image references: @Image2/);assert.match(r.warning('@Image1 @Audio1',{supports_image_input:false,supports_audio_input:false},{}),/Unsupported reference type/);
assert.equal(JSON.stringify(options),before);assert.equal(r.label('anything','image',0),'@Image1');
console.log('PASS: '+fixtures.length+' shared recognition fixtures; provider-independent grammar, supported types/modes, missing files, offsets and immutable options');
