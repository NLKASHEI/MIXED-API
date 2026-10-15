package com.mixedapi.local;

import java.text.SimpleDateFormat;
import java.util.ArrayDeque;
import java.util.Date;
import java.util.Locale;

/** In-memory, bounded diagnostics. Never store user messages or authorization headers. */
public final class BridgeLogs {
    private static final ArrayDeque<String> EVENTS = new ArrayDeque<>();
    private BridgeLogs() {}
    public static synchronized void add(int code,String backend,String path,String detail){
        String stamp=new SimpleDateFormat("HH:mm:ss",Locale.getDefault()).format(new Date());
        String hint=detail==null?"":detail.replace('\n',' ').replace('\r',' ');
        if(hint.length()>150) hint=hint.substring(0,150)+"…";
        // Never include upstream body or secrets; only call with stable error labels.
        EVENTS.addFirst(stamp+"   "+code+"   "+backend+"  "+path+(hint.isEmpty()?"":"\n  "+hint));
        while(EVENTS.size()>60)EVENTS.removeLast();
    }
    public static synchronized String text(){
        if(EVENTS.isEmpty())return "暂无请求。\n\n连接模型后，这里会显示接口状态和报错。";
        StringBuilder out=new StringBuilder();for(String s:EVENTS)out.append(s).append("\n\n");return out.toString();
    }
    public static synchronized void clear(){EVENTS.clear();}
}
