package com.mixedapi.local;

import android.app.Activity;
import android.os.Bundle;
import android.view.ViewGroup;
import android.webkit.WebView;
import android.widget.TextView;

/** The official source page has its own WebView profile, never the console's. */
public final class GoogleSourceActivity extends Activity {
  private WebView page;

  @Override
  public void onCreate(Bundle state) {
    super.onCreate(state);
    try {
      page = GoogleSessions.shared().view(getIntent().getStringExtra("source"));
      if (page.getParent() instanceof ViewGroup) ((ViewGroup) page.getParent()).removeView(page);
      setContentView(page);
    } catch (Exception e) {
      TextView error = new TextView(this);
      error.setTextSize(18);
      error.setPadding(24, 48, 24, 24);
      error.setText("来源窗口未准备好，请返回配置重试。");
      setContentView(error);
    }
  }

  @Override
  protected void onDestroy() {
    if (page != null && page.getParent() instanceof ViewGroup)
      ((ViewGroup) page.getParent()).removeView(page);
    super.onDestroy();
  }
}
