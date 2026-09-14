import exec from 'k6/execution';
import ws from 'k6/ws';
import { check } from 'k6';
import { Trend } from 'k6/metrics';

// 1. Define a custom trend metric for latency
const wsMsgLatency = new Trend('ws_msg_latency');

export const options = {
    vus: 10,
    duration: '10s',
    thresholds: {
        // Optional: Fail the test if 95% of messages take longer than 200ms
        'ws_msg_latency': ['p(95)<200'],
    },
};

export default function () {
    const url = `ws://127.0.0.1:8080/ws?user_id=${__VU}&skip_history=true`;
    const totalVus = exec.instance.vusInitialized;

    const res = ws.connect(url, {}, function (socket) {
        socket.on('open', function () {
            socket.setTimeout(function () {
                console.log(`[user-${__VU}] 5 seconds passed. Safely closing the socket.`);
                socket.close();
            }, 5000);

            for (let i = 1; i <= 10; i++) {
                socket.setTimeout(function () {
                    const randomVu = Math.floor(Math.random() * totalVus) + 1;

                    const payload = JSON.stringify({
                        id: crypto.randomUUID(),
                        author_id: `${__VU}`,
                        destination_user_id: `${randomVu}`,
                        message: Date.now().toString(),
                    });

                    socket.send(payload);
                }, i * 500);
            }
        });

        socket.on('message', function (payload) {
            const now = Date.now();
            const rtt = now - Number(JSON.parse(payload).message);

            wsMsgLatency.add(rtt);

            console.log(`[user-${__VU}] Received ${payload} in ${rtt}ms`);
        });
    });

    check(res, {
        'status is 101': (r) => {
            if (r.status === 101) {
                return true
            }

            console.log(r);

            return false;
        }
    });
}
