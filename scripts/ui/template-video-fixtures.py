"""Generate distinct local clips and retain independent timing/hash evidence."""
import hashlib,json,subprocess,sys
from pathlib import Path
out=Path(sys.argv[1]);out.mkdir(parents=True,exist_ok=True)
records=[]
for name,color,rate,duration in [('first','red','23',3),('second','green','30',13),('third','blue','30',3),('cfr24','yellow','24',3),('cfr23976','purple','24000/1001',3)]:
    file=out/(name+'.mp4')
    subprocess.run(['ffmpeg','-nostdin','-v','error','-f','lavfi','-i',f'color=c={color}:s=540x960:r={rate}', '-t',str(duration),'-c:v','libx264','-pix_fmt','yuv420p','-movflags','+faststart','-y',str(file)],check=True)
    probe=json.loads(subprocess.check_output(['ffprobe','-v','error','-select_streams','v:0','-show_entries','stream=codec_name,width,height,r_frame_rate,avg_frame_rate,time_base,duration,nb_frames:packet=duration_time','-of','json',str(file)]))
    records.append({'file':file.name,'sha256':hashlib.sha256(file.read_bytes()).hexdigest(),'bytes':file.stat().st_size,'probe':probe})
(out/'manifest.json').write_text(json.dumps(records,indent=2))
print('Generated five synthetic clips; no user media or provider call.')
