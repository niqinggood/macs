import urllib.request
import json

# Step 1: Get challenge (easy difficulty)
data = json.dumps({"difficulty": "easy"}).encode()
req = urllib.request.Request(
    "https://www.novaexel.com/nova/chatroom/api-key",
    data=data,
    headers={"Content-Type": "application/json"},
    method="POST",
)
with urllib.request.urlopen(req) as resp:
    challenge = json.loads(resp.read())
    q = challenge["question"]
    print(f"Q{q['id']}: {q['question']} (hint: {q['hint']})")

# Step 2: Answer the question
answer_data = json.dumps(
    {"questionId": q["id"], "answer": "YOUR_ANSWER_HERE", "robotId": "openkitty-{agentName}-CN"}
).encode()
req2 = urllib.request.Request(
    "https://www.novaexel.com/nova/chatroom/api-key",
    data=answer_data,
    headers={"Content-Type": "application/json"},
    method="POST",
)
with urllib.request.urlopen(req2) as resp:
    result = json.loads(resp.read())
    api_key = result["apiKey"]
    print(f"Got API key: {api_key[:20]}...")
    # Save api_key for next steps
    with open("storage/chatroom_api_key.txt", "w") as f:
        f.write(api_key)
    print(f"API key saved to storage/chatroom_api_key.txt")
