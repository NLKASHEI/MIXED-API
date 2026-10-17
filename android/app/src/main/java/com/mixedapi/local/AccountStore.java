package com.mixedapi.local;

import android.content.Context;
import android.security.keystore.KeyGenParameterSpec;
import android.security.keystore.KeyProperties;
import android.util.AtomicFile;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.nio.charset.StandardCharsets;
import java.security.KeyStore;
import java.util.Arrays;
import javax.crypto.Cipher;
import javax.crypto.KeyGenerator;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import org.json.JSONObject;

/** App-private, backup-excluded credential files sealed with an Android Keystore key. */
final class AccountStore {
  interface Keys {
    SecretKey get() throws Exception;
  }

  private static final String ALIAS = "mixed.accounts.v1";
  private final File directory;
  private final Keys keys;

  AccountStore(Context context, Keys keys) {
    directory = new File(context.getNoBackupFilesDir(), "accounts");
    this.keys = keys == null ? AccountStore::platformKey : keys;
  }

  private static synchronized SecretKey platformKey() throws Exception {
    KeyStore store = KeyStore.getInstance("AndroidKeyStore");
    store.load(null);
    if (store.containsAlias(ALIAS)) return (SecretKey) store.getKey(ALIAS, null);
    KeyGenerator generator =
        KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore");
    generator.init(
        new KeyGenParameterSpec.Builder(
                ALIAS, KeyProperties.PURPOSE_ENCRYPT | KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
            .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256)
            .build());
    return generator.generateKey();
  }

  private AtomicFile file(String id) throws Exception {
    if (!NativeAccounts.source(id)) throw new java.io.IOException("Invalid account source");
    if (!directory.isDirectory() && !directory.mkdirs())
      throw new java.io.IOException("Account directory unavailable");
    return new AtomicFile(new File(directory, id + ".dat"));
  }

  JSONObject read(String id) throws Exception {
    byte[] data;
    try (InputStream in = file(id).openRead()) {
      data = BridgeHttpServer.bounded(in, 1024 * 1024);
    }
    if (data.length < 29 || data[0] != 1) throw new java.io.IOException("Invalid account file");
    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
    cipher.init(
        Cipher.DECRYPT_MODE,
        keys.get(),
        new GCMParameterSpec(128, Arrays.copyOfRange(data, 1, 13)));
    cipher.updateAAD(id.getBytes(StandardCharsets.UTF_8));
    return new JSONObject(
        new String(cipher.doFinal(data, 13, data.length - 13), StandardCharsets.UTF_8));
  }

  void write(String id, JSONObject value) throws Exception {
    byte[] raw = value.toString().getBytes(StandardCharsets.UTF_8);
    if (raw.length > 900000) throw new java.io.IOException("Account data too large");
    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
    cipher.init(Cipher.ENCRYPT_MODE, keys.get());
    cipher.updateAAD(id.getBytes(StandardCharsets.UTF_8));
    byte[] sealed = cipher.doFinal(raw);
    AtomicFile file = file(id);
    FileOutputStream out = null;
    try {
      out = file.startWrite();
      out.write(1);
      out.write(cipher.getIV());
      out.write(sealed);
      file.finishWrite(out);
    } catch (Exception e) {
      if (out != null) file.failWrite(out);
      throw e;
    }
  }

  void delete(String id) throws Exception {
    file(id).delete();
  }
}
