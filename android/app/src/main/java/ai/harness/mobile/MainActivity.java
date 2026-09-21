package ai.harness.mobile;

import android.app.Activity;
import android.os.*;
import android.content.SharedPreferences;
import android.widget.*;
import java.io.*;
import java.net.*;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.*;
import org.json.*;

public class MainActivity extends Activity {
    EditText endpoint, token, provider, model, message;
    TextView log;
    ExecutorService io = Executors.newSingleThreadExecutor();
    Handler ui = new Handler(Looper.getMainLooper());
    String session="", job=""; long after=0;

    @Override public void onCreate(Bundle b) {
        super.onCreate(b);
        LinearLayout root=new LinearLayout(this); root.setOrientation(LinearLayout.VERTICAL); root.setPadding(20,20,20,20);
        endpoint=field("Cloudflare Worker URL"); token=field("Client token"); provider=field("Provider"); model=field("Model"); message=field("Message");
        provider.setText("opencode");
        root.addView(endpoint); root.addView(token); root.addView(provider); root.addView(model);
        LinearLayout row=new LinearLayout(this);
        Button save=button("Save"), fresh=button("New"), stop=button("Stop");
        row.addView(save,new LinearLayout.LayoutParams(0,-2,1)); row.addView(fresh,new LinearLayout.LayoutParams(0,-2,1)); row.addView(stop,new LinearLayout.LayoutParams(0,-2,1));
        root.addView(row);
        ScrollView scroll=new ScrollView(this); log=new TextView(this); scroll.addView(log); root.addView(scroll,new LinearLayout.LayoutParams(-1,0,1));
        LinearLayout send=new LinearLayout(this); send.addView(message,new LinearLayout.LayoutParams(0,-2,1)); Button go=button("Send"); send.addView(go); root.addView(send);
        setContentView(root); load();
        save.setOnClickListener(v -> save()); fresh.setOnClickListener(v -> createSession()); stop.setOnClickListener(v -> cancel()); go.setOnClickListener(v -> send());
    }
    EditText field(String hint){EditText e=new EditText(this);e.setHint(hint);e.setSingleLine(false);return e;}
    Button button(String s){Button b=new Button(this);b.setText(s);return b;}
    void load(){SharedPreferences p=getSharedPreferences("harness",0);endpoint.setText(p.getString("endpoint",""));token.setText(p.getString("token",""));provider.setText(p.getString("provider","opencode"));model.setText(p.getString("model",""));session=p.getString("session","");}
    void save(){getSharedPreferences("harness",0).edit().putString("endpoint",endpoint.getText().toString().trim()).putString("token",token.getText().toString().trim()).putString("provider",provider.getText().toString().trim()).putString("model",model.getText().toString().trim()).putString("session",session).apply();}
    void createSession(){io.execute(()->{try{JSONObject b=new JSONObject();b.put("provider",provider.getText().toString().trim());b.put("model",model.getText().toString().trim());session=request("POST","/v1/sessions",b.toString()).getString("id");after=0;save();add("session "+session);}catch(Exception e){add("new: "+e.getMessage());}});}
    void send(){String text=message.getText().toString().trim();if(text.isEmpty())return;io.execute(()->{try{if(session.isEmpty()){createSession();}JSONObject b=new JSONObject();b.put("message",text);String m=model.getText().toString().trim();if(!m.isEmpty())b.put("model",m);JSONObject o=request("POST","/v1/sessions/"+session+"/messages",b.toString());job=o.getString("job_id");after=0;add("you: "+text);message.post(()->message.setText(""));poll();}catch(Exception e){add("send: "+e.getMessage());}});}
    void poll(){if(job.isEmpty())return;try{JSONObject ev=request("GET","/v1/jobs/"+job+"/events?after="+after,null);JSONArray a=ev.getJSONArray("events");for(int i=0;i<a.length();i++){JSONObject e=a.getJSONObject(i);after=e.getLong("id");String t=e.optString("type");JSONObject p=e.optJSONObject("payload");if("trace".equals(t)&&p!=null)add("["+p.optString("stage","trace")+"] "+p.toString());else if("subagent_report".equals(t))add("[subagent] "+String.valueOf(e.opt("payload")));else if("completed".equals(t))add("completed");}JSONObject state=request("GET","/v1/jobs/"+job,null);String s=state.optString("status","");if("queued".equals(s)||"running".equals(s))ui.postDelayed(this::poll,800);else{add("status: "+s);job="";}}catch(Exception e){add("poll: "+e.getMessage());ui.postDelayed(this::poll,1500);}}
    void cancel(){if(job.isEmpty())return;io.execute(()->{try{request("POST","/v1/jobs/"+job+"/cancel","{}");add("cancel requested");}catch(Exception e){add("cancel: "+e.getMessage());}});}
    JSONObject request(String method,String path,String body)throws Exception{String base=endpoint.getText().toString().trim().replaceAll("/+$","");HttpURLConnection c=(HttpURLConnection)new URL(base+path).openConnection();c.setRequestMethod(method);c.setConnectTimeout(15000);c.setReadTimeout(30000);c.setRequestProperty("Authorization","Bearer "+token.getText().toString().trim());c.setRequestProperty("Content-Type","application/json");if(body!=null){c.setDoOutput(true);try(OutputStream o=c.getOutputStream()){o.write(body.getBytes(StandardCharsets.UTF_8));}}int code=c.getResponseCode();InputStream in=code>=400?c.getErrorStream():c.getInputStream();String text=read(in);c.disconnect();if(code<200||code>=300)throw new Exception("HTTP "+code+": "+text);return text.isEmpty()?new JSONObject():new JSONObject(text);}
    String read(InputStream in)throws Exception{if(in==null)return "";try(BufferedReader r=new BufferedReader(new InputStreamReader(in,StandardCharsets.UTF_8))){StringBuilder b=new StringBuilder();String line;while((line=r.readLine())!=null)b.append(line);return b.toString();}}
    void add(String s){runOnUiThread(()->log.append(s+"\n"));}
    @Override protected void onDestroy(){io.shutdownNow();super.onDestroy();}
}
