// Disposable PNGs only; no shipped provider artwork or user uploads.
const zlib=require('node:zlib');
function crc(bytes){let c=0xffffffff;for(const b of bytes){c^=b;for(let i=0;i<8;i++)c=(c>>>1)^((c&1)?0xedb88320:0);}return (c^0xffffffff)>>>0;}
function chunk(type,data){const t=Buffer.from(type),b=Buffer.alloc(data.length+12);b.writeUInt32BE(data.length);t.copy(b,4);data.copy(b,8);b.writeUInt32BE(crc(Buffer.concat([t,data])),b.length-4);return b;}
function make({depth=8,type=6,pixels=[255,255,255,0,255,255,255,127,255,255,255,255],width=3,height=1,palette=false}={}){
 const ihdr=Buffer.alloc(13);ihdr.writeUInt32BE(width);ihdr.writeUInt32BE(height,4);ihdr[8]=depth;ihdr[9]=type;
 const chunks=[chunk('IHDR',ihdr)];if(palette){chunks.push(chunk('PLTE',Buffer.from([255,255,255,255,255,255,255,255,255])),chunk('tRNS',Buffer.from([0,127,255])));}
 chunks.push(chunk('IDAT',zlib.deflateSync(Buffer.from([0,...pixels]))),chunk('IEND',Buffer.alloc(0)));
 return Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),...chunks]);
}
module.exports={make,valid:make(),palette:make({type:3,palette:true,pixels:[0,1,2]}),opaqueRGB:make({type:2,pixels:Array(9).fill(255)}),opaqueRGBA:make({pixels:Array(12).fill(255)}),nearOpaque16:make({depth:16,width:1,pixels:[255,255,255,255,255,255,255,254]}),opaque16:make({depth:16,width:1,pixels:Array(8).fill(255)})};
