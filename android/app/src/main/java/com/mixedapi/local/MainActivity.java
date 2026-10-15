package com.mixedapi.local;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.widget.ArrayAdapter;
import android.widget.Button;
import android.widget.CheckBox;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.Spinner;
import android.widget.TextView;
import org.json.JSONArray;
import org.json.JSONObject;
import java.net.HttpURLConnection;
import java.net.URL;
import java.io.InputStream;
import java.io.ByteArrayOutputStream;

/** Real Android-native control center. Web admin is optional, never required. */
public final class MainActivity extends Activity {
    private static final String API="http://127.0.0.1:8765/v1";
    private final Handler handler=new Handler(Looper.getMainLooper());
    private LinearLayout content;
    private TextView live,notice,logs;
    private Spinner provider;
    private EditText upstreamUrl,upstreamKey;
    private CheckBox enabled;
    private JSONArray configured;
    private final int blue=Color.rgb(33,99,192),dark=Color.rgb(28,42,64),muted=Color.rgb(78,92,111);

    private int dp(float n){return Math.round(n*getResources().getDisplayMetrics().density);}
    private GradientDrawable surface(int bg,int stroke,int radius){
        GradientDrawable d=new GradientDrawable();d.setColor(bg);d.setCornerRadius(dp(radius));if(stroke!=0)d.setStroke(dp(1),stroke);return d;
    }
    private LinearLayout vertical(){LinearLayout a=new LinearLayout(this);a.setOrientation(LinearLayout.VERTICAL);return a;}
    private void margin(View v,int t){LinearLayout.LayoutParams p=new LinearLayout.LayoutParams(-1,-2);p.topMargin=dp(t);v.setLayoutParams(p);}
    private TextView label(String text,int sp,boolean bold){
        TextView t=new TextView(this);t.setText(text);t.setTextColor(dark);t.setTextSize(sp);if(bold)t.setTypeface(Typeface.DEFAULT,Typeface.BOLD);t.setLineSpacing(dp(2),1);return t;
    }
    private TextView paragraph(String text){TextView t=label(text,16,false);t.setTextColor(muted);return t;}
    private LinearLayout card(){
        LinearLayout v=vertical();v.setPadding(dp(18),dp(18),dp(18),dp(18));v.setBackground(surface(Color.WHITE,Color.rgb(223,230,239),16));margin(v,16);content.addView(v);return v;
    }
    private Button button(LinearLayout parent,String title,Runnable cb,boolean primary){
        Button b=new Button(this);b.setText(title);b.setAllCaps(false);b.setTextSize(16);b.setTypeface(Typeface.DEFAULT,Typeface.BOLD);
        b.setTextColor(primary?Color.WHITE:blue);b.setBackground(surface(primary?blue:Color.rgb(234,242,255),0,12));
        margin(b,10);parent.addView(b);b.setOnClickListener(v->cb.run());return b;
    }
    private void copy(String title,String value){
        ((ClipboardManager)getSystemService(CLIPBOARD_SERVICE)).setPrimaryClip(ClipData.newPlainText(title,value));notice.setText("已复制："+title);
    }
    private void message(String text){notice.setText(text);}
    private EditText input(LinearLayout parent,String placeholder){
        EditText field=new EditText(this);field.setTextSize(17);field.setSingleLine(true);field.setHint(placeholder);
        field.setBackground(surface(Color.rgb(245,248,253),Color.rgb(221,230,240),10));field.setPadding(dp(12),dp(12),dp(12),dp(12));
        margin(field,8);parent.addView(field);return field;
    }
    @Override public void onCreate(Bundle b){super.onCreate(b);
        getWindow().setStatusBarColor(Color.rgb(244,247,252));getWindow().setNavigationBarColor(Color.rgb(244,247,252));
        getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR|View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR);
        ScrollView scroll=new ScrollView(this);scroll.setFillViewport(true);scroll.setBackgroundColor(Color.rgb(244,247,252));
        content=vertical();content.setPadding(dp(20),dp(27),dp(20),dp(35));scroll.addView(content);setContentView(scroll);
        content.addView(label("MIXED-API",29,true));TextView sub=paragraph("一个地址 · 一个 Key · 全部可用模型");margin(sub,5);content.addView(sub);

        LinearLayout service=card();service.addView(label("本地服务",21,true));
        live=label("● 正在检查…",19,true);margin(live,12);service.addView(live);
        TextView base=paragraph(API);margin(base,8);service.addView(base);
        notice=paragraph("先启动服务，再配置需要的上游。");margin(notice,8);service.addView(notice);
        button(service,"▶ 启动本地服务",()->{
            try {startForegroundService(new Intent(this,BridgeService.class));message("正在启动本地代理…");}
            catch(Exception e){message("启动失败："+e.getMessage());BridgeLogs.add(500,"system","start",e.getClass().getSimpleName());}
        },true);
        button(service,"复制统一 API 地址",()->copy("API Base URL",API),false);
        button(service,"复制客户端 Key",()->{
            try{copy("客户端 API Key",BridgeConfig.load(this).getString("api_key"));}
            catch(Exception e){message("密钥读取失败："+e.getMessage());}
        },false);
        button(service,"停止服务",()->{stopService(new Intent(this,BridgeService.class));message("服务已停止");},false);

        LinearLayout settings=card();settings.addView(label("连接上游",21,true));
        TextView tip=paragraph("推荐先连接电脑 MIXED-API：手机可以直接使用电脑上的 Codex、WorkBuddy 等模型。其他可访问的 HTTP 上游也能在这里连接。");margin(tip,8);settings.addView(tip);
        try{configured=BridgeConfig.load(this).getJSONArray("backends");}catch(Exception e){message("配置读取失败："+e.getMessage());configured=new JSONArray();}
        String[] labels=new String[configured.length()];for(int i=0;i<labels.length;i++){JSONObject obj=configured.optJSONObject(i);labels[i]=obj==null?"未知":obj.optString("name",obj.optString("id"));}
        provider=new Spinner(this);ArrayAdapter<String> adapter=new ArrayAdapter<>(this,android.R.layout.simple_spinner_dropdown_item,labels);provider.setAdapter(adapter);margin(provider,12);settings.addView(provider);
        upstreamUrl=input(settings,"上游 URL，例如 http://192.168.1.20:8766/v1");
        upstreamKey=input(settings,"上游 Key（需要时才填）");upstreamKey.setInputType(129);
        enabled=new CheckBox(this);enabled.setText("启用这个来源");enabled.setTextSize(17);margin(enabled,9);settings.addView(enabled);
        provider.setOnItemSelectedListener(new android.widget.AdapterView.OnItemSelectedListener(){
            @Override public void onItemSelected(android.widget.AdapterView<?> a,View v,int p,long id){loadProvider(p);}
            @Override public void onNothingSelected(android.widget.AdapterView<?> a){}
        });
        button(settings,"保存来源设置",()->saveProvider(),true);
        button(settings,"检查可用模型",()->loadModels(),false);

        LinearLayout monitor=card();monitor.addView(label("请求日志 / 错误详情",21,true));
        TextView hint=paragraph("只记录接口、状态码和错误原因；不会保存对话正文或 API Key。");margin(hint,8);monitor.addView(hint);
        logs=label("等待请求…",16,false);logs.setTypeface(Typeface.MONOSPACE);logs.setTextIsSelectable(true);logs.setPadding(dp(4),dp(12),dp(4),dp(14));margin(logs,5);monitor.addView(logs);
        button(monitor,"清空日志",()->{BridgeLogs.clear();logs.setText(BridgeLogs.text());},false);
        LinearLayout extras=card();extras.addView(label("其他",20,true));
        button(extras,"检查更新",()->startActivity(new Intent(Intent.ACTION_VIEW,Uri.parse("https://github.com/NLKASHEI/MIXED-API/releases/latest"))),false);
        button(extras,"高级网页设置（可选）",()->startActivity(new Intent(Intent.ACTION_VIEW,Uri.parse("http://127.0.0.1:8765/"))),false);
        handler.postDelayed(new Runnable(){public void run(){if(isFinishing())return;live.setText(BridgeService.running?"● 正在运行":"● 未启动");live.setTextColor(BridgeService.running?Color.rgb(20,140,93):Color.rgb(192,70,53));logs.setText(BridgeLogs.text());handler.postDelayed(this,1800);}},300);
    }
    private void loadProvider(int pos){
        try{JSONObject p=configured.getJSONObject(pos);upstreamUrl.setText(p.optString("url"));upstreamKey.setText(p.optString("key"));enabled.setChecked(p.optBoolean("enabled"));}catch(Exception ignored){}
    }
    private void saveProvider(){
        try{
            JSONObject cfg=BridgeConfig.load(this);JSONArray arr=cfg.getJSONArray("backends");int i=provider.getSelectedItemPosition();if(i<0||i>=arr.length())return;
            JSONObject p=arr.getJSONObject(i);String url=upstreamUrl.getText().toString().trim();
            if(!url.startsWith("http://")&&!url.startsWith("https://")){message("地址必须以 http:// 或 https:// 开头");return;}
            p.put("url",url);p.put("key",upstreamKey.getText().toString().trim());p.put("auth",upstreamKey.getText().length()>0?"bearer":"none");p.put("enabled",enabled.isChecked());
            BridgeConfig.save(this,cfg);configured=cfg.getJSONArray("backends");message("已保存！点「检查可用模型」验证上游连接。");
        }catch(Exception e){message("保存失败："+e.getMessage());}
    }
    private void loadModels(){
        message("正在检查可用模型（可能需要几秒）…");
        new Thread(()->{
            String result;
            HttpURLConnection c=null;
            try{
                String key=BridgeConfig.load(this).getString("api_key");
                c=(HttpURLConnection)new URL(API+"/models").openConnection();c.setConnectTimeout(5000);c.setReadTimeout(15000);c.setRequestProperty("Authorization","Bearer "+key);
                int code=c.getResponseCode();InputStream in=code<400?c.getInputStream():c.getErrorStream();ByteArrayOutputStream out=new ByteArrayOutputStream();byte[] buf=new byte[4096];int n;while((n=in.read(buf))>0&&out.size()<300000)out.write(buf,0,n);
                JSONObject response=new JSONObject(out.toString("UTF-8"));if(code>=400)throw new Exception("HTTP "+code);
                int count=response.optJSONArray("data")==null?0:response.getJSONArray("data").length();
                result="检测完成："+count+" 个可用模型。";
                JSONObject errors=response.optJSONObject("backend_errors");if(errors!=null&&errors.length()>0){result+=" 部分上游未连接："+errors.toString();}
                BridgeLogs.add(200,"models","/v1/models","共 "+count+" 个模型");
            }catch(Exception e){result="检查失败："+e.getMessage();BridgeLogs.add(502,"models","/v1/models",e.getClass().getSimpleName());}
            finally{if(c!=null)c.disconnect();}
            final String msg=result;runOnUiThread(()->message(msg));
        },"ModelCheck").start();
    }
}
