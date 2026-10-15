package com.mixedapi.local;

import android.content.Context;
import android.content.SharedPreferences;
import org.json.JSONArray;
import org.json.JSONObject;
import java.security.SecureRandom;
import java.net.URI;

/** Stores private JSON in app sandbox; deliberately does not contain provider login credentials by default. */
public final class BridgeConfig {
    private static final String PREF = "mixed_config";
    private static final String ENTRY = "state";
    private BridgeConfig() {}

    private static String key(String prefix) {
        byte[] bytes = new byte[24];new SecureRandom().nextBytes(bytes);
        StringBuilder out = new StringBuilder(prefix);
        for(byte b:bytes)out.append(String.format(java.util.Locale.ROOT,"%02x",b & 0xff));
        return out.toString();
    }
    private static JSONObject backend(String id, String name, String url, String auth) throws Exception {
        JSONObject obj = new JSONObject();obj.put("id",id);obj.put("name",name);
        obj.put("url",url);obj.put("key","");obj.put("auth",auth);obj.put("enabled",false);
        obj.put("command","");obj.put("workdir","");return obj;
    }
    public static synchronized JSONObject load(Context ctx) throws Exception {
        SharedPreferences p=ctx.getSharedPreferences(PREF,Context.MODE_PRIVATE);
        String raw=p.getString(ENTRY,null);
        if(raw!=null){
            JSONObject existing=new JSONObject(raw);
            JSONArray backends=existing.optJSONArray("backends");
            if(backends!=null){
                boolean foundPC=false;
                for(int i=0;i<backends.length();i++){
                    JSONObject b=backends.optJSONObject(i);
                    if(b==null)continue;
                    if("pc".equals(b.optString("id")))foundPC=true;
                    if("codex".equals(b.optString("id"))) b.put("name","Codex (通过电脑共享)");
                }
                if(!foundPC){backends.put(backend("pc","电脑 MIXED-API（优先推荐）","http://192.168.1.100:8766/v1","bearer"));save(ctx,existing);}
            }
            return existing;
        }
        JSONObject obj=new JSONObject();obj.put("listen","127.0.0.1:8765");
        obj.put("api_key",key("sk-mix-"));obj.put("admin_key",key("admin-"));
        JSONArray a=new JSONArray();
        a.put(backend("vertex","Vertex Express / Cookie","http://127.0.0.1:8050/v1","bearer"));
        a.put(backend("codex","Codex (通过电脑共享)","http://127.0.0.1:8901/codex/v1","none"));
        a.put(backend("opencode","OpenCode 2API","http://127.0.0.1:8080/v1","bearer"));
        a.put(backend("workbuddy","WorkBuddy Proxy","http://127.0.0.1:8790/v1","bearer"));
        a.put(backend("aps","APS2api","http://127.0.0.1:2156/v1","bearer"));
        a.put(backend("aistudio","AI Studio Build","http://127.0.0.1:7860/v1","bearer"));
        a.put(backend("pc","电脑 MIXED-API（优先推荐）","http://192.168.1.100:8766/v1","bearer"));
        obj.put("backends",a);save(ctx,obj);return obj;
    }
    public static synchronized void save(Context ctx, JSONObject obj) throws Exception {
        if(!"127.0.0.1:8765".equals(obj.optString("listen")))throw new Exception("Android only binds loopback:8765");
        String api=obj.optString("api_key"),admin=obj.optString("admin_key");
        if(api.isEmpty()||admin.isEmpty()||api.equals(admin))throw new Exception("Invalid keys");
        JSONArray arr=obj.getJSONArray("backends");java.util.HashSet<String> ids=new java.util.HashSet<>();
        for(int i=0;i<arr.length();i++){
            JSONObject b=arr.getJSONObject(i);String id=b.getString("id"),auth=b.optString("auth"),src=b.getString("url");
            if(!id.matches("[a-zA-Z0-9_-]+")||!ids.add(id))throw new Exception("Invalid backend ID");
            if(!auth.equals("none")&&!auth.equals("bearer")&&!auth.equals("x-api-key"))throw new Exception("Invalid backend auth");
            URI u=new URI(src);if(u.getHost()==null||(!"http".equals(u.getScheme())&&!"https".equals(u.getScheme()))||u.getUserInfo()!=null||u.getRawQuery()!=null||u.getRawFragment()!=null)throw new Exception("Invalid URL: "+src);
        }
        if(!ctx.getSharedPreferences(PREF,Context.MODE_PRIVATE).edit().putString(ENTRY,obj.toString()).commit())throw new Exception("Cannot save settings");
    }
}
