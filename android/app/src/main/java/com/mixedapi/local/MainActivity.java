package com.mixedapi.local;

import android.app.Activity;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Intent;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.view.View;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import org.json.JSONObject;

/** Native launcher: proxy configuration lives in bundled same-origin management UI. */
public final class MainActivity extends Activity {
    private LinearLayout root;
    private TextView info;
    private Button button(String caption,View.OnClickListener cb){Button b=new Button(this);b.setText(caption);b.setAllCaps(false);b.setOnClickListener(cb);root.addView(b);return b;}
    private void copy(String label,String value){ClipboardManager cb=(ClipboardManager)getSystemService(CLIPBOARD_SERVICE);cb.setPrimaryClip(ClipData.newPlainText(label,value));info.setText("已复制："+label);}
    @Override public void onCreate(Bundle state){super.onCreate(state);
        ScrollView scroll=new ScrollView(this);root=new LinearLayout(this);root.setOrientation(LinearLayout.VERTICAL);root.setPadding(32,32,32,40);scroll.addView(root);setContentView(scroll);
        TextView title=new TextView(this);title.setText("MIXED-API\n统一本地中转站");title.setTextSize(26f);root.addView(title);
        TextView desc=new TextView(this);desc.setText("六个上游 → 一个 URL、一个 Key。首次启动自动生成两套不同的密钥。\n\n统一 API: http://127.0.0.1:8765/v1\n模型名示例：codex/gpt-6-luna\n\n手机单机不能运行 Windows 的 Codex CLI / WorkBuddy。推荐：电脑双击 Start-MIXED-API-LAN.cmd，手机打开管理后台，启用「电脑 MIXED-API」，填写 http://电脑局域网IP:8766/v1 和电脑的客户端 API Key。\n电脑与手机连接同一可信局域网。电脑管理后台始终只在电脑本机打开。\n");root.addView(desc);
        info=new TextView(this);info.setText("请点击启动服务");root.addView(info);
        button("▶ 启动本地代理",v->{try{Intent x=new Intent(this,BridgeService.class);startForegroundService(x);info.setText("正在启动。稍等片刻打开管理面板。");}catch(Exception e){info.setText(e.toString());}});
        button("⚙ 打开管理后台",v->{Intent i=new Intent(Intent.ACTION_VIEW,Uri.parse("http://127.0.0.1:8765/"));startActivity(i);});
        button("📋 复制统一 API 地址",v->copy("API Base URL","http://127.0.0.1:8765/v1"));
        button("🔑 复制客户端 API Key",v->{try{JSONObject c=BridgeConfig.load(this);copy("MIXED-API Key",c.getString("api_key"));}catch(Exception e){info.setText(e.toString());}});
        button("🛡 复制管理后台 Admin Key",v->{try{JSONObject c=BridgeConfig.load(this);copy("Admin Key",c.getString("admin_key"));}catch(Exception e){info.setText(e.toString());}});
        button("■ 停止本地代理",v->{stopService(new Intent(this,BridgeService.class));info.setText("已停止本地代理");});
        try{BridgeConfig.load(this);}catch(Exception e){info.setText("配置初始化失败："+e);}
    }
}
