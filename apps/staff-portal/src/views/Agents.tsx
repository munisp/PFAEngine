import { useEffect, useState } from 'react';
import { Api } from '../api';

// Agent network oversight: accreditation status, enrolment counts,
// commission leaderboard (agents engine).
export default function Agents({ api }: { api: Api }) {
  const [board, setBoard] = useState<any[]>([]);
  const [msg, setMsg] = useState('');

  function load() {
    api.agentsLeaderboard().then(r => {
      const rows = Object.entries(r.leaderboard ?? {}).map(([agent, count]) => ({ agent, count }));
      rows.sort((a, b) => b.count - a.count);
      setBoard(rows);
    }).catch(e => setMsg(String(e)));
  }
  useEffect(load, []);

  return (
    <section>
      <h2>Agent network</h2>
      <button onClick={load}>Refresh</button>
      {board.length === 0 && <p>No agents yet.</p>}
      <table style={{ width: '100%', marginTop: 12 }}>
        <thead><tr><th>#</th><th>Agent</th><th>Enrolments</th></tr></thead>
        <tbody>
          {board.map((a: any, i: number) => (
            <tr key={a.agent}>
              <td>{i + 1}</td>
              <td>{a.agent}</td>
              <td>{a.count}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {msg && <p>{msg}</p>}
    </section>
  );
}
