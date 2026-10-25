#!/usr/bin/env python3
import socket,socketserver,threading,select
class Proxy(socketserver.BaseRequestHandler):
 def handle(self):
  upstream=socket.create_connection(('172.24.0.4',3000),timeout=10)
  try:
   sockets=[self.request,upstream]
   while True:
    readable,_,_=select.select(sockets,[],[],180)
    if not readable:return
    for sock in readable:
     data=sock.recv(65536)
     if not data:return
     (upstream if sock is self.request else self.request).sendall(data)
  finally:upstream.close()
class Server(socketserver.ThreadingTCPServer):
 allow_reuse_address=True
 daemon_threads=True
Server(('127.0.0.1',3018),Proxy).serve_forever()
