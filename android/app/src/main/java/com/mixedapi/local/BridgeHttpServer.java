package com.mixedapi.local;

import android.content.Context;
import android.util.Log;
import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedInputStream;
import java.io.BufferedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.IOException;
import java.net.HttpURLConnection;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URI;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicBoolean;

/**
 * Small Android loopback HTTP/1.1 reverse proxy. Uses no WebView fetch bridge,
 * so responses are usable by any Android app that connects to 127.0.0.1.
 * Only supports Content-Length request bodies. No HTTP/2, multipart or WebSocket.
 */
public final class BridgeHttpServer implements AutoCloseable {
    private final Context context;
    private final ExecutorService pool=Executors.newFixedThreadPool(12);
    private final AtomicBoolean open=new AtomicBoolean(false);
    private ServerSocket socket;
    private Thread acceptor;
    private static final int MAX_BODY=32*1024*1024;

    public BridgeHttpServer(Context ctx){context=ctx.getApplicationContext();}
    public synchronized void start()throws Exception{
        if(open.get())return;
        BridgeConfig.load(context);
        socket=new ServerSocket(8765,50,InetAddress.getByName("127.0.0.1"));open.set(true);
        acceptor=new Thread(()->{
            while(open.get()){
                try{Socket connection=socket.accept();pool.execute(()->process(connection));}
                catch(Exception ex){if(open.get())Log.e("MIXED-API","Accept failed",ex);}
            }
        },"MixedApiAccept");acceptor.setDaemon(true);acceptor.start();
    }
    @Override public synchronized void close(){open.set(false);try{if(socket!=null)socket.close();}catch(Exception ignored){}pool.shutdownNow();}
    private static byte[] bounded(InputStream in,int max)throws IOException{
        ByteArrayOutputStream b=new ByteArrayOutputStream();byte[] tmp=new byte[8192];int n;
        while((n=in.read(tmp))!=-1){if(b.size()+n>max)throw new IOException("Response too large");b.write(tmp,0,n);}
        return b.toByteArray();
    }
    private static String line(BufferedInputStream in)throws IOException{
        ByteArrayOutputStream b=new ByteArrayOutputStream();int c;
        while((c=in.read())!=-1){if(c=='\n')break;if(c!='\r')b.write(c);if(b.size()>16384)throw new IOException("Header too long");}
        if(c==-1&&b.size()==0)return null;
        return b.toString("UTF-8");
    }
    private static byte[] read(BufferedInputStream in,int len)throws IOException{
        byte[] result=new byte[len];int pos=0;while(pos<len){int n=in.read(result,pos,len-pos);if(n<0)throw new IOException("Unexpected EOF");pos+=n;}return result;
    }
    private static String status(int code){switch(code){case 200:return "OK";case 201:return "Created";case 400:return "Bad Request";case 401:return "Unauthorized";case 404:return "Not Found";case 405:return "Method Not Allowed";case 411:return "Length Required";case 413:return "Payload Too Large";case 422:return "Unprocessable Entity";case 500:return "Internal Server Error";case 501:return "Not Implemented";case 502:return "Bad Gateway";case 503:return "Service Unavailable";default:return "Status";}}
    private static void response(OutputStream out,int code,String type,byte[] body)throws IOException{
        String head="HTTP/1.1 "+code+" "+status(code)+"\r\nContent-Type: "+type+"\r\nContent-Length: "+body.length+"\r\nConnection: close\r\nCache-Control: no-store\r\nX-Content-Type-Options: nosniff\r\n\r\n";
        out.write(head.getBytes(StandardCharsets.UTF_8));out.write(body);out.flush();
    }
    private static void json(OutputStream out,int status,JSONObject data)throws IOException{response(out,status,"application/json; charset=utf-8",data.toString().getBytes(StandardCharsets.UTF_8));}
    private static void fail(OutputStream out,int status,String message)throws IOException{
        try{json(out,status,new JSONObject().put("error",new JSONObject().put("message",message).put("type","mixed_api_error")));}
        catch(Exception e){response(out,status,"text/plain; charset=utf-8",message.getBytes(StandardCharsets.UTF_8));}
    }
    private static boolean authorized(Map<String,String> headers,String key){String value=headers.getOrDefault("authorization","");
        if(value.startsWith("Bearer "))value=value.substring(7);else value=headers.getOrDefault("x-admin-key","");
        return key!=null&&java.security.MessageDigest.isEqual(value.getBytes(StandardCharsets.UTF_8),key.getBytes(StandardCharsets.UTF_8));
    }
    private static HttpURLConnection connection(JSONObject b,String path,String method)throws Exception{
        URI base=new URI(b.getString("url"));
        URI dst=new URI(base.getScheme(),null,base.getHost(),base.getPort(),base.getPath().replaceAll("/+$","")+path,null,null);
        HttpURLConnection c=(HttpURLConnection)new URL(dst.toString()).openConnection();
        c.setConnectTimeout(6000);c.setReadTimeout(240000);c.setRequestMethod(method);c.setInstanceFollowRedirects(false);
        String key=b.optString("key"),auth=b.optString("auth");
        if(!key.isEmpty()){
            if("bearer".equals(auth))c.setRequestProperty("Authorization","Bearer "+key);
            else if("x-api-key".equals(auth))c.setRequestProperty("x-api-key",key);
        }
        c.setRequestProperty("Accept","application/json, text/event-stream");return c;
    }
    private static JSONArray enabled(JSONObject config)throws Exception{return config.getJSONArray("backends");}
    private void process(Socket peer){
        try(Socket s=peer){s.setSoTimeout(20000);
            BufferedInputStream in=new BufferedInputStream(s.getInputStream());BufferedOutputStream out=new BufferedOutputStream(s.getOutputStream());
            String first=line(in);if(first==null)return;String[] request=first.split(" ");
            if(request.length<3){fail(out,400,"Invalid request line");return;}
            String method=request[0],path=request[1];Map<String,String> headers=new LinkedHashMap<>();
            String l;while((l=line(in))!=null&&!l.isEmpty()){
                int index=l.indexOf(':');if(index>0)headers.put(l.substring(0,index).trim().toLowerCase(Locale.ROOT),l.substring(index+1).trim());
                if(headers.size()>100){fail(out,400,"Too many headers");return;}
            }
            if("chunked".equalsIgnoreCase(headers.getOrDefault("transfer-encoding",""))){fail(out,411,"Chunked request bodies not supported");return;}
            int len=0;try{len=Integer.parseInt(headers.getOrDefault("content-length","0"));}catch(NumberFormatException e){fail(out,400,"Invalid content length");return;}
            if(len<0||len>MAX_BODY){fail(out,413,"Body too large");return;}
            byte[] body=read(in,len);
            JSONObject config=BridgeConfig.load(context);
            if("/".equals(path)&&"GET".equals(method)){
                try(InputStream asset=context.getAssets().open("index.html")){response(out,200,"text/html; charset=utf-8",bounded(asset,2*1024*1024));}return;
            }
            if("/health".equals(path)){json(out,200,new JSONObject().put("ok",true).put("service","MIXED-API"));return;}
            if(path.startsWith("/api/")){
                if(!authorized(headers,config.getString("admin_key"))){fail(out,401,"Admin key required");return;}
                admin(out,method,path,body,config);return;
            }
            if(!path.startsWith("/v1/")){fail(out,404,"Not found");return;}
            if(!authorized(headers,config.getString("api_key"))){fail(out,401,"Invalid MIXED-API key");return;}
            if("/v1/models".equals(path)&&"GET".equals(method)){models(out,config);return;}
            if("POST".equals(method)&& ("/v1/chat/completions".equals(path)||"/v1/responses".equals(path)||"/v1/embeddings".equals(path)||"/v1/messages".equals(path)||"/v1/messages/count_tokens".equals(path))){forward(out,path,body,config,headers);return;}
            fail(out,404,"Unsupported endpoint");
        }catch(Exception ex){Log.e("MIXED-API","Request failed",ex);}
    }
    private void admin(OutputStream out,String method,String path,byte[] body,JSONObject config)throws Exception{
        if("/api/config".equals(path)&&"GET".equals(method)){json(out,200,config);return;}
        if("/api/guides".equals(path)&&"GET".equals(method)){json(out,200,new JSONObject());return;}
        if("/api/config".equals(path)&&"PUT".equals(method)){
            try{JSONObject next=new JSONObject(new String(body,StandardCharsets.UTF_8));
                if(!config.getString("api_key").equals(next.optString("api_key"))||!config.getString("admin_key").equals(next.optString("admin_key")))throw new Exception("Cannot change keys in UI");
                BridgeConfig.save(context,next);json(out,200,new JSONObject().put("saved",true));
            }catch(Exception e){fail(out,400,e.getMessage());}return;
        }
        if(path.startsWith("/api/process/")){fail(out,501,"Android cannot execute desktop startup commands; run the upstream independently");return;}
        if("/api/status".equals(path)){json(out,200,new JSONObject().put("processes",new JSONObject()).put("backends",config.getJSONArray("backends")));return;}
        fail(out,404,"Unknown admin API");
    }
    private void models(OutputStream out,JSONObject cfg)throws Exception{
        JSONArray all=new JSONArray();JSONObject errors=new JSONObject();JSONArray bs=enabled(cfg);
        for(int i=0;i<bs.length();i++){
            JSONObject b=bs.getJSONObject(i);if(!b.optBoolean("enabled"))continue;
            HttpURLConnection c=null;
            try{
                c=connection(b,"/models","GET");c.setReadTimeout(8000);int status=c.getResponseCode();
                if(status!=200){errors.put(b.getString("id"),"HTTP "+status);continue;}
                byte[] data=bounded(c.getInputStream(),5*1024*1024);
                JSONArray original=new JSONObject(new String(data,StandardCharsets.UTF_8)).getJSONArray("data");
                for(int k=0;k<original.length();k++){
                    String model=original.getJSONObject(k).optString("id");
                    if(!model.isEmpty()) {
                        // PC gateway already prefixes models (codex/gpt, workbuddy/...),
                        // so preserve those IDs exactly rather than adding pc/ in Android.
                        String publicID="pc".equals(b.optString("id"))?model:b.getString("id")+"/"+model;
                        all.put(new JSONObject().put("id",publicID).put("object","model").put("owned_by",b.getString("id")).put("original_id",model));
                    }
                }
            }catch(Exception e){errors.put(b.getString("id"),"Unavailable or invalid model list");}
            finally{if(c!=null)c.disconnect();}
        }
        json(out,200,new JSONObject().put("object","list").put("data",all).put("backend_errors",errors));
    }
    private void forward(OutputStream out,String path,byte[] body,JSONObject cfg,Map<String,String> headers)throws Exception{
        JSONObject input;try{input=new JSONObject(new String(body,StandardCharsets.UTF_8));}catch(Exception e){fail(out,400,"Invalid JSON body");return;}
        String model=input.optString("model");int slash=model.indexOf('/');
        if(slash<1||slash==model.length()-1){fail(out,400,"Expected model: backend/model");return;}
        String id=model.substring(0,slash),upstreamModel=model.substring(slash+1);
        JSONObject b=null;JSONArray bs=enabled(cfg);for(int i=0;i<bs.length();i++){
            JSONObject item=bs.getJSONObject(i);if(item.optBoolean("enabled")&&id.equals(item.optString("id"))){b=item;break;}
        }
        if(b==null){
            // Forward any model advertised by the desktop gateway directly,
            // preserving codex/gpt, workbuddy/model and other existing prefixes.
            for(int i=0;i<bs.length();i++){
                JSONObject item=bs.getJSONObject(i);
                if(item.optBoolean("enabled")&&"pc".equals(item.optString("id"))){b=item;break;}
            }
            if(b==null){fail(out,404,"Backend not enabled: "+id+"; enable the PC bridge or the backend locally");return;}
            upstreamModel=model;
        }
        input.put("model",upstreamModel);byte[] packet=input.toString().getBytes(StandardCharsets.UTF_8);
        HttpURLConnection c=null;boolean started=false;
        try{
            c=connection(b,path.substring(3),"POST");c.setDoOutput(true);c.setRequestProperty("Content-Type","application/json");
            for(String field:new String[]{"anthropic-version","anthropic-beta","openai-beta"})if(headers.containsKey(field))c.setRequestProperty(field,headers.get(field));
            c.setFixedLengthStreamingMode(packet.length);
            try(OutputStream upload=c.getOutputStream()){upload.write(packet);}
            int status=c.getResponseCode();String content=c.getContentType();if(content==null||content.contains("\r")||content.contains("\n"))content="application/json";
            String header="HTTP/1.1 "+status+" "+status(status)+"\r\nContent-Type: "+content+"\r\nTransfer-Encoding: chunked\r\nConnection: close\r\nCache-Control: no-store\r\nX-Mixed-Backend: "+id+"\r\n\r\n";
            out.write(header.getBytes(StandardCharsets.UTF_8));out.flush();started=true;
            try(InputStream resp=status>=400?c.getErrorStream():c.getInputStream()){
                if(resp!=null){byte[] buf=new byte[16384];int n;while((n=resp.read(buf))!=-1){if(n==0)continue;out.write((Integer.toHexString(n)+"\r\n").getBytes(StandardCharsets.US_ASCII));out.write(buf,0,n);out.write("\r\n".getBytes(StandardCharsets.US_ASCII));out.flush();}}
            }
            out.write("0\r\n\r\n".getBytes(StandardCharsets.US_ASCII));out.flush();
        }catch(Exception e){Log.e("MIXED-API","Upstream error: "+id,e);if(!started){try{fail(out,502,"Backend unavailable: "+id);}catch(Exception ignored){}}}
        finally{if(c!=null)c.disconnect();}
    }
}
