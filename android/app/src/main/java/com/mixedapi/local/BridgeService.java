package com.mixedapi.local;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.os.Build;
import android.os.IBinder;
import android.util.Log;

public final class BridgeService extends Service {
    public static volatile boolean running=false;
    private BridgeHttpServer server;
    @Override public void onCreate(){super.onCreate();}
    @Override public int onStartCommand(Intent intent,int flags,int startId){
        if(intent!=null&&"stop".equals(intent.getAction())){stopSelf();return START_NOT_STICKY;}
        NotificationManager nm=getSystemService(NotificationManager.class);
        nm.createNotificationChannel(new NotificationChannel("mixed_api_running","MIXED-API 本地服务",NotificationManager.IMPORTANCE_LOW));
        Intent open=new Intent(this,MainActivity.class);
        PendingIntent pending=PendingIntent.getActivity(this,0,open,PendingIntent.FLAG_IMMUTABLE|PendingIntent.FLAG_UPDATE_CURRENT);
        Notification n=new Notification.Builder(this,"mixed_api_running")
                .setSmallIcon(android.R.drawable.ic_menu_share).setContentTitle("MIXED-API 正在运行")
                .setContentText("本地接口 http://127.0.0.1:8765/v1")
                .setContentIntent(pending).setOngoing(true).build();
        startForeground(2048,n);
        if(server==null){server=new BridgeHttpServer(this);try{server.start();running=true;BridgeLogs.add(200,"system","start","本地端口 8765 正在监听");}catch(Exception ex){running=false;BridgeLogs.add(500,"system","start","端口启动失败");Log.e("MIXED-API","Server failed",ex);stopSelf();}}
        return START_NOT_STICKY;
    }
    @Override public void onDestroy(){running=false;if(server!=null){server.close();server=null;}GoogleSessions.shared().close();super.onDestroy();}
    @Override public IBinder onBind(Intent intent){return null;}
}
