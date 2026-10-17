package com.mixedapi.local;

import android.app.Activity;
import android.content.Intent;
import android.graphics.Color;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.TextView;

/** Shared, responsive console; no JavaScript/native credential bridge. */
public final class MainActivity extends Activity {
  private final Handler handler = new Handler(Looper.getMainLooper());
  private WebView console;
  private boolean destroyed;
  private android.webkit.ValueCallback<Uri[]> fileSelection;
  private static final int ACCOUNT_FILE = 410;

  @Override
  public void onCreate(Bundle state) {
    super.onCreate(state);
    getWindow().setStatusBarColor(Color.rgb(247, 248, 245));
    getWindow().setNavigationBarColor(Color.rgb(247, 248, 245));
    getWindow()
        .getDecorView()
        .setSystemUiVisibility(
            android.view.View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR
                | android.view.View.SYSTEM_UI_FLAG_LIGHT_NAVIGATION_BAR);
    TextView loading = new TextView(this);
    loading.setText("MIXED\n正在连接本机工作台…");
    loading.setTextSize(20);
    loading.setPadding(30, 70, 30, 30);
    setContentView(loading);
    try {
      startForegroundService(new Intent(this, BridgeService.class));
      awaitService(0);
    } catch (Exception e) {
      loading.setText("服务未启动，请重新打开应用。\n" + e.getClass().getSimpleName());
    }
  }

  private void awaitService(int attempt) {
    if (destroyed) return;
    if (BridgeService.running) {
      showConsole();
      return;
    }
    if (attempt >= 40) {
      TextView error = new TextView(this);
      error.setText("本机服务启动失败。请检查 8765 端口，或重新打开应用。");
      error.setPadding(30, 70, 30, 30);
      setContentView(error);
      return;
    }
    handler.postDelayed(() -> awaitService(attempt + 1), 250);
  }

  private void openExternal(Uri uri) {
    if ("https".equals(uri.getScheme()))
      try {
        startActivity(new Intent(Intent.ACTION_VIEW, uri));
      } catch (Exception ignored) {
      }
  }

  private void showConsole() {
    console = new WebView(this);
    WebSettings settings = console.getSettings();
    settings.setJavaScriptEnabled(true);
    settings.setDomStorageEnabled(true);
    settings.setAllowFileAccess(false);
    settings.setAllowContentAccess(false);
    settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
    console.setWebViewClient(
        new WebViewClient() {
          @Override
          public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
            Uri uri = request.getUrl();
            if ("http".equals(uri.getScheme())
                && "127.0.0.1".equals(uri.getHost())
                && uri.getPort() == 8765) return false;
            openExternal(uri);
            return true;
          }
        });
    // User-clicked target=_blank links remain outside the privileged console.
    settings.setSupportMultipleWindows(true);
    console.setWebChromeClient(
        new WebChromeClient() {
          @Override
          public boolean onShowFileChooser(
              WebView view,
              android.webkit.ValueCallback<Uri[]> callback,
              FileChooserParams params) {
            Uri origin = Uri.parse(view.getUrl() == null ? "" : view.getUrl());
            if (!"http".equals(origin.getScheme())
                || !"127.0.0.1".equals(origin.getHost())
                || origin.getPort() != 8765) return false;
            if (fileSelection != null) fileSelection.onReceiveValue(null);
            fileSelection = callback;
            Intent choose =
                new Intent(Intent.ACTION_OPEN_DOCUMENT)
                    .addCategory(Intent.CATEGORY_OPENABLE)
                    .setType("application/json");
            try {
              startActivityForResult(choose, ACCOUNT_FILE);
            } catch (Exception e) {
              fileSelection = null;
              callback.onReceiveValue(null);
            }
            return true;
          }

          @Override
          public boolean onCreateWindow(
              WebView view, boolean dialog, boolean gesture, android.os.Message result) {
            if (!gesture) return false;
            WebView link = new WebView(MainActivity.this);
            link.setWebViewClient(
                new WebViewClient() {
                  @Override
                  public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                    openExternal(r.getUrl());
                    v.destroy();
                    return true;
                  }
                });
            ((WebView.WebViewTransport) result.obj).setWebView(link);
            result.sendToTarget();
            return true;
          }
        });
    setContentView(console);
    try {
      String key = BridgeConfig.load(this).getString("admin_key");
      console.loadUrl("http://127.0.0.1:8765/#admin=" + key);
    } catch (Exception e) {
      console.loadUrl("http://127.0.0.1:8765/");
    }
  }

  @Override
  protected void onActivityResult(int request, int result, Intent data) {
    super.onActivityResult(request, result, data);
    if (request != ACCOUNT_FILE || fileSelection == null) return;
    android.webkit.ValueCallback<Uri[]> callback = fileSelection;
    fileSelection = null;
    Uri uri = result == RESULT_OK && data != null ? data.getData() : null;
    callback.onReceiveValue(
        uri != null && "content".equals(uri.getScheme()) ? new Uri[] {uri} : null);
  }

  @Override
  public void onBackPressed() {
    if (console != null && console.canGoBack()) console.goBack();
    else super.onBackPressed();
  }

  @Override
  protected void onDestroy() {
    destroyed = true;
    handler.removeCallbacksAndMessages(null);
    if (fileSelection != null) {
      fileSelection.onReceiveValue(null);
      fileSelection = null;
    }
    if (console != null) console.destroy();
    super.onDestroy();
  }
}
