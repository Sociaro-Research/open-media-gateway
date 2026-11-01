// Decimal display units only. Admission always compares the original byte count.
window.FileSize=(()=>{
 'use strict';
 const number=(value,digits)=>new Intl.NumberFormat('en-US',{maximumFractionDigits:digits}).format(value);
 const valid=bytes=>Number.isSafeInteger(bytes)&&bytes>=0;
 function format(bytes,approximate=false){
  if(!valid(bytes))return '';
  const units=['B','KB','MB','GB','TB'];let value=bytes,index=0;
  while(value>=1000&&index<units.length-1){value/=1000;index++;}
  if(Number(value.toFixed(2))>=1000&&index<units.length-1){value/=1000;index++;}
  return (approximate&&Number(value.toFixed(2))!==value?'≈':'')+number(value,2)+' '+units[index];
 }
 function megabytes(bytes){
  if(!valid(bytes))return '';
  const value=bytes/1_000_000,digits=value>=1||value===0?2:Math.min(6,Math.max(2,Math.ceil(-Math.log10(value))+1));
  return (Number(value.toFixed(digits))!==value?'≈':'')+number(value,digits)+' MB';
 }
 const exact=bytes=>valid(bytes)?number(bytes,0)+' bytes':'';
 return {format,limit:bytes=>format(bytes,true),megabytes,exact};
})();
