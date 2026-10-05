# verify-wrkq feature 9 webhook sink: python3 sink.py <port> <out.jsonl>
import http.server, sys, json
out = sys.argv[2]
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        with open(out, 'a') as f:
            f.write(json.dumps({"path": self.path, "body": body.decode()}) + "\n")
        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')
    def log_message(self, *a): pass
http.server.HTTPServer(('127.0.0.1', int(sys.argv[1])), H).serve_forever()
