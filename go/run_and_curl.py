import subprocess
import time
import urllib.request
import json
import os

# 1. Kill any existing zcode-proxy using exact process name
os.system("pkill -9 -x zcode-proxy || true")
time.sleep(1)

# 2. Start zcode-proxy serve
log_file = open("/tmp/zcode.log", "w")
proc = subprocess.Popen(
    ["/home/mhankbarbar/zcode-proxy/zcode-proxy", "serve", "-config", "/home/mhankbarbar/zcode-proxy/config.yaml"],
    stdout=log_file,
    stderr=subprocess.STDOUT,
    cwd="/home/mhankbarbar/zcode-proxy"
)
print(f"Started zcode-proxy PID: {proc.pid}")
time.sleep(3)

# 3. Send curl request to glm-5.3-flash
url = "http://127.0.0.1:8081/v1/chat/completions"
req_data = {
    "model": "glm-5.3-flash",
    "messages": [
        {"role": "user", "content": "Halo! Jawab dengan kalimat singkat: GLM 5.3 Flash Siap Digunakan!"}
    ],
    "stream": True
}

headers = {
    "Content-Type": "application/json",
    "Authorization": "Bearer joydazo"
}

req = urllib.request.Request(url, data=json.dumps(req_data).encode("utf-8"), headers=headers)

print("\n--- STREAMING RESPONSE FROM GLM-5.3-FLASH ---")
try:
    with urllib.request.urlopen(req, timeout=30) as response:
        for line in response:
            line_str = line.decode("utf-8")
            print(line_str, end="", flush=True)
except Exception as e:
    print(f"\nRequest error: {e}")

print("\n\n--- PROXY LOGS ---")
log_file.flush()
with open("/tmp/zcode.log") as f:
    print(f.read())

proc.terminate()
