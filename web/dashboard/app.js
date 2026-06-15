// MiniChart drawing implementation using HTML5 Canvas
class MiniChart {
    constructor(canvasId, colors, maxPoints = 30) {
        this.canvas = document.getElementById(canvasId);
        if (!this.canvas) return;
        this.ctx = this.canvas.getContext('2d');
        this.colors = Array.isArray(colors) ? colors : [colors];
        this.maxPoints = maxPoints;
        this.data = [];
        this.resize();
        window.addEventListener('resize', () => this.resize());
    }

    resize() {
        if (!this.canvas) return;
        const dpr = window.devicePixelRatio || 1;
        const rect = this.canvas.getBoundingClientRect();
        this.canvas.width = rect.width * dpr;
        this.canvas.height = rect.height * dpr;
        this.ctx.scale(dpr, dpr);
        this.width = rect.width;
        this.height = rect.height;
        this.draw();
    }

    addData(val) {
        this.data.push(val);
        if (this.data.length > this.maxPoints) {
            this.data.shift();
        }
        this.draw();
    }

    draw() {
        if (!this.canvas || this.data.length < 2) return;
        const ctx = this.ctx;
        ctx.clearRect(0, 0, this.width, this.height);

        // Find max value to auto-scale Y-axis
        let maxVal = 0.001;
        this.data.forEach(item => {
            if (Array.isArray(item)) {
                item.forEach(v => { if (v > maxVal) maxVal = v; });
            } else {
                if (item > maxVal) maxVal = item;
            }
        });
        maxVal = maxVal * 1.15; // 15% margin

        const numSeries = Array.isArray(this.data[0]) ? this.data[0].length : 1;

        for (let s = 0; s < numSeries; s++) {
            const seriesData = Array.isArray(this.data[0]) ? this.data.map(d => d[s]) : this.data;
            const color = this.colors[s % this.colors.length];
            this.drawSeries(seriesData, color, maxVal);
        }
    }

    drawSeries(points, color, maxVal) {
        const ctx = this.ctx;
        const stepX = this.width / (this.maxPoints - 1);

        ctx.beginPath();
        points.forEach((val, idx) => {
            const x = idx * stepX;
            const y = this.height - (val / maxVal) * (this.height - 20) - 10;
            if (idx === 0) {
                ctx.moveTo(x, y);
            } else {
                ctx.lineTo(x, y);
            }
        });

        // Glowing line effect
        ctx.shadowColor = color;
        ctx.shadowBlur = 6;
        ctx.strokeStyle = color;
        ctx.lineWidth = 2;
        ctx.stroke();
        ctx.shadowBlur = 0; // reset shadow

        // Fill area below the line with gradient
        ctx.lineTo((points.length - 1) * stepX, this.height);
        ctx.lineTo(0, this.height);
        ctx.closePath();

        const grad = ctx.createLinearGradient(0, 0, 0, this.height);
        grad.addColorStop(0, color + '20'); // very transparent
        grad.addColorStop(1, color + '00');
        ctx.fillStyle = grad;
        ctx.fill();
    }
}

// Global Dashboard App state
const App = {
    currentNodeId: null,
    uptimeSeconds: 0,
    prevRequestsCount: 0,
    prevTimestamp: 0,
    
    // Charts instances
    throughputChart: null,
    latencyChart: null,
    
    init() {
        this.throughputChart = new MiniChart('throughput-chart', '#10b981');
        // green: p50, yellow: p95, indigo: p99
        this.latencyChart = new MiniChart('latency-chart', ['#10b981', '#f59e0b', '#6366f1']);

        // Start polling loops
        this.poll();
        setInterval(() => this.poll(), 1500);
    },

    async poll() {
        try {
            const [status, peers, metricsText] = await Promise.all([
                fetch('/v1/cluster/status').then(res => res.json()),
                fetch('/v1/cluster/peers').then(res => res.json()),
                fetch('/metrics').then(res => res.text())
            ]);

            this.updateHeader(status);
            this.updatePeersTable(peers);
            this.updateTopology(status, peers);
            this.updateMetricsCharts(metricsText);
        } catch (err) {
            console.error("Dashboard poll failed:", err);
        }
    },

    updateHeader(status) {
        this.currentNodeId = status.node_id;
        document.getElementById('node-id').textContent = status.node_id;
        document.getElementById('node-role').textContent = status.role;
        document.getElementById('store-size').textContent = status.store_size + " keys";

        // Format uptime
        const hours = Math.floor(status.uptime_seconds / 3600);
        const mins = Math.floor((status.uptime_seconds % 3600) / 60);
        const secs = Math.floor(status.uptime_seconds % 60);
        
        let uptimeStr = "";
        if (hours > 0) uptimeStr += `${hours}h `;
        if (mins > 0 || hours > 0) uptimeStr += `${mins}m `;
        uptimeStr += `${secs}s`;
        document.getElementById('uptime').textContent = uptimeStr;
    },

    updatePeersTable(peers) {
        const tbody = document.getElementById('peers-list');
        if (peers.length === 0) {
            tbody.innerHTML = `<tr><td colspan="6" class="empty-state">No peers detected...</td></tr>`;
            return;
        }

        tbody.innerHTML = peers.map(p => {
            const stateClass = `badge ${p.state}`;
            const rtt = p.rtt_ms ? (p.rtt_ms / 1e6).toFixed(2) + " ms" : "-";
            const lag = p.replication_lag !== undefined ? p.replication_lag + " ops" : "-";
            const lastSeen = p.last_seen ? new Date(p.last_seen).toLocaleTimeString() : "-";
            
            return `
                <tr>
                    <td style="font-family: var(--font-mono); font-weight: 500;">${p.node_id}</td>
                    <td>${p.address}</td>
                    <td><span class="${stateClass}">${p.state}</span></td>
                    <td style="font-family: var(--font-mono);">${rtt}</td>
                    <td style="font-family: var(--font-mono);">${lag}</td>
                    <td>${lastSeen}</td>
                </tr>
            `;
        }).join('');
    },

    updateTopology(status, peers) {
        const svg = document.getElementById('topology-svg');
        const connGroup = document.getElementById('connections-group');
        const nodesGroup = document.getElementById('nodes-group');
        
        if (!svg) return;

        // Clean groups
        connGroup.innerHTML = '';
        nodesGroup.innerHTML = '';

        const width = 500;
        const height = 400;
        const cx = width / 2;
        const cy = height / 2;

        // Filter and build nodes list: Coordinator is always in the center
        const nodes = [{
            node_id: status.node_id,
            address: 'localhost:' + window.location.port,
            state: 'alive',
            role: status.role,
            x: cx,
            y: cy,
            isSelf: true
        }];

        // Positions peers in a circle around center coordinator
        const otherPeers = peers.filter(p => p.node_id !== status.node_id);
        const numPeers = otherPeers.length;

        otherPeers.forEach((p, idx) => {
            const angle = (idx * (2 * Math.PI)) / numPeers;
            const radius = 120;
            nodes.push({
                node_id: p.node_id,
                address: p.address,
                state: p.state,
                role: 'peer',
                x: cx + radius * Math.cos(angle),
                y: cy + radius * Math.sin(angle),
                isSelf: false
            });
        });

        // 1. Draw connection lines
        nodes.forEach(n => {
            if (n.isSelf) return;

            // Draw line from center node to peer
            const line = document.createElementNS("http://www.w3.org/2000/svg", "line");
            line.setAttribute("x1", cx);
            line.setAttribute("y1", cy);
            line.setAttribute("x2", n.x);
            line.setAttribute("y2", n.y);
            
            let strokeColor = "rgba(255,255,255,0.06)";
            if (n.state === 'alive') {
                line.setAttribute("class", "node-link replicate");
            } else {
                line.setAttribute("class", "node-link");
            }
            connGroup.appendChild(line);
        });

        // 2. Draw node circles and labels
        nodes.forEach(n => {
            const g = document.createElementNS("http://www.w3.org/2000/svg", "g");
            g.setAttribute("class", "node-circle");

            // Glow filter / radial gradient background
            const glow = document.createElementNS("http://www.w3.org/2000/svg", "circle");
            glow.setAttribute("cx", n.x);
            glow.setAttribute("cy", n.y);
            glow.setAttribute("r", n.isSelf ? 26 : 20);
            
            let color = "#10b981"; // success
            if (n.state === 'suspect') color = "#f59e0b";
            if (n.state === 'dead') color = "#ef4444";

            glow.setAttribute("fill", `url(#glow-${n.state})`);
            g.appendChild(glow);

            // Core circle
            const circle = document.createElementNS("http://www.w3.org/2000/svg", "circle");
            circle.setAttribute("cx", n.x);
            circle.setAttribute("cy", n.y);
            circle.setAttribute("r", n.isSelf ? 14 : 10);
            circle.setAttribute("fill", "#090d16");
            circle.setAttribute("stroke", color);
            circle.setAttribute("stroke-width", n.isSelf ? "3" : "2.5");
            g.appendChild(circle);

            // Labels
            const label = document.createElementNS("http://www.w3.org/2000/svg", "text");
            label.setAttribute("x", n.x);
            label.setAttribute("y", n.y + (n.isSelf ? 34 : 26));
            label.setAttribute("text-anchor", "middle");
            label.setAttribute("class", "node-label");
            label.textContent = n.node_id;
            g.appendChild(label);

            const sublabel = document.createElementNS("http://www.w3.org/2000/svg", "text");
            sublabel.setAttribute("x", n.x);
            sublabel.setAttribute("y", n.y + (n.isSelf ? 46 : 36));
            sublabel.setAttribute("text-anchor", "middle");
            sublabel.setAttribute("class", "node-sublabel");
            sublabel.textContent = n.address;
            g.appendChild(sublabel);

            nodesGroup.appendChild(g);
        });
    },

    updateMetricsCharts(metricsText) {
        const metrics = this.parsePrometheusMetrics(metricsText);
        const now = Date.now();

        // 1. Throughput (ops/sec)
        let totalRequests = 0;
        if (metrics['http_requests_total']) {
            metrics['http_requests_total'].forEach(m => {
                totalRequests += m.value;
            });
        }

        if (this.prevRequestsCount > 0 && this.prevTimestamp > 0) {
            const elapsedSeconds = (now - this.prevTimestamp) / 1000;
            if (elapsedSeconds > 0) {
                const ops = (totalRequests - this.prevRequestsCount) / elapsedSeconds;
                this.throughputChart.addData(ops);
            }
        } else {
            this.throughputChart.addData(0);
        }
        this.prevRequestsCount = totalRequests;
        this.prevTimestamp = now;

        // 2. Latency Percentiles (p50, p95, p99)
        const percentiles = this.calculatePercentiles(metrics);
        this.latencyChart.addData(percentiles); // passes [p50, p95, p99]
    },

    parsePrometheusMetrics(text) {
        const lines = text.split('\n');
        const metrics = {};
        
        lines.forEach(line => {
            if (line.startsWith('#') || !line.trim()) return;
            // Parse metric name, labels and value
            const match = line.match(/^([a-zA-Z_0-9]+)({[^}]+})?\s+([0-9e.+-]+)/);
            if (match) {
                const name = match[1];
                const labelsStr = match[2];
                const value = parseFloat(match[3]);
                const labels = {};
                
                if (labelsStr) {
                    const labelMatches = labelsStr.slice(1, -1).matchAll(/([a-zA-Z_0-9]+)="([^"]*)"/g);
                    for (const lm of labelMatches) {
                        labels[lm[1]] = lm[2];
                    }
                }
                
                if (!metrics[name]) metrics[name] = [];
                metrics[name].push({ labels, value });
            }
        });
        
        return metrics;
    },

    calculatePercentiles(metrics) {
        const bucketMetric = metrics['http_request_duration_seconds_bucket'];
        if (!bucketMetric) {
            return [0, 0, 0];
        }

        // Aggregate counts per bucket limit (le)
        const buckets = {};
        let totalRequests = 0;

        bucketMetric.forEach(b => {
            const le = b.labels.le;
            if (le) {
                const limit = le === '+Inf' ? Infinity : parseFloat(le);
                buckets[limit] = (buckets[limit] || 0) + b.value;
                if (limit === Infinity) {
                    totalRequests += b.value;
                }
            }
        });

        if (totalRequests === 0) {
            return [0, 0, 0];
        }

        // Convert to sorted array of {limit, count}
        const bucketList = Object.keys(buckets).map(k => {
            const lim = parseFloat(k);
            return {
                limit: isNaN(lim) ? Infinity : lim,
                count: buckets[k]
            };
        }).sort((a, b) => a.limit - b.limit);

        // Function to interpolate value at percentile P
        const getPercentile = (p) => {
            const target = p * totalRequests;
            let prevLimit = 0;
            let prevCount = 0;

            for (let i = 0; i < bucketList.length; i++) {
                const b = bucketList[i];
                if (b.count >= target) {
                    if (b.limit === Infinity) {
                        return prevLimit * 1.5; // fallback approximation for unbounded bucket
                    }
                    const ratio = (target - prevCount) / (b.count - prevCount || 1);
                    const valSecs = prevLimit + (b.limit - prevLimit) * ratio;
                    return valSecs * 1000; // convert to ms
                }
                prevLimit = b.limit;
                prevCount = b.count;
            }
            return 0;
        };

        return [getPercentile(0.50), getPercentile(0.95), getPercentile(0.99)];
    }
};

// Start dashboard app on load
window.addEventListener('DOMContentLoaded', () => App.init());
