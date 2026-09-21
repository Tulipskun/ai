# AI Harness Android frontend

The APK is a frontend only. It stores the Cloudflare endpoint, client token, selected provider/model, and the active session ID locally.

Requests go to the Cloudflare control plane at POST /v1/sessions/{session_id}/messages.

Cloudflare D1 stores sessions and jobs. Kaggle workers claim queued jobs and run the existing Go agent loop. Kaggle is not used as durable storage.

Open the android directory as a Gradle Android project in AIDE. No third-party Android dependencies are required.
