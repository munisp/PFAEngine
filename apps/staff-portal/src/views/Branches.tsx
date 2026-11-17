import { useCallback, useEffect, useState } from 'react';
import { Api } from '../api';
import type { Appt, Branch, BranchInput } from '../api';

// Branches & appointments — branch directory management plus the per-day
// appointment board with status transitions (check-in → done / no-show / cancel).
export default function Branches({ api }: { api: Api }) {
  const [branches, setBranches] = useState<Branch[]>([]);
  const [sel, setSel] = useState('');
  const [date, setDate] = useState(new Date().toISOString().slice(0, 10));
  const [appts, setAppts] = useState<Appt[]>([]);
  const [showForm, setShowForm] = useState(false);
  const [form, setForm] = useState<BranchInput>({
    name: '', address: '', city: '', state: '', phone: '', email: '', open_hours: 'Mon–Fri 9:00–16:00',
  });
  const [err, setErr] = useState('');

  const refresh = useCallback(async () => {
    try {
      const r = await api.branchesList();
      setBranches(r.branches);
      if (!sel && r.branches.length > 0) setSel(r.branches[0].id);
      setErr('');
    } catch (e: any) { setErr(e.message); }
  }, [api, sel]);

  useEffect(() => { refresh(); }, [refresh]);

  const loadAppts = useCallback(async () => {
    if (!sel) { setAppts([]); return; }
    try { setAppts((await api.appointmentsList(sel, date)).appointments); setErr(''); }
    catch (e: any) { setErr(e.message); }
  }, [api, sel, date]);

  useEffect(() => { loadAppts(); }, [loadAppts]);

  const addBranch = async () => {
    try {
      await api.branchAdd({
        ...form,
        slot_minutes: form.slot_minutes || undefined,
        slots_per_day: form.slots_per_day || undefined,
      });
      setForm({ name: '', address: '', city: '', state: '', phone: '', email: '', open_hours: 'Mon–Fri 9:00–16:00' });
      setShowForm(false);
      setErr('');
      await refresh();
    } catch (e: any) { setErr(e.message); }
  };

  const transition = async (id: string, to: string) => {
    try { await api.apptTransition(id, to); setErr(''); await loadAppts(); }
    catch (e: any) { setErr(e.message); }
  };

  return (
    <section>
      <h2>Branches &amp; appointments</h2>
      {err && <p style={{ color: 'crimson' }}>{err}</p>}

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3>Branches</h3>
        <button onClick={() => setShowForm(!showForm)}>{showForm ? 'Hide form' : 'Add branch'}</button>
      </div>
      {showForm && (
        <div style={{ border: '1px solid #e5e7eb', borderRadius: 8, padding: 12, marginBottom: 12, display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          <input placeholder="Name" value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} />
          <input placeholder="Address" value={form.address} onChange={e => setForm({ ...form, address: e.target.value })} />
          <input placeholder="City" value={form.city} onChange={e => setForm({ ...form, city: e.target.value })} />
          <input placeholder="State" value={form.state} onChange={e => setForm({ ...form, state: e.target.value })} />
          <input placeholder="Phone" value={form.phone} onChange={e => setForm({ ...form, phone: e.target.value })} />
          <input placeholder="Email" value={form.email} onChange={e => setForm({ ...form, email: e.target.value })} />
          <input placeholder="Open hours" value={form.open_hours} onChange={e => setForm({ ...form, open_hours: e.target.value })} />
          <input placeholder="Slot minutes" type="number" value={form.slot_minutes ?? ''} onChange={e => setForm({ ...form, slot_minutes: e.target.value ? Number(e.target.value) : undefined })} style={{ width: 100 }} />
          <input placeholder="Slots/day" type="number" value={form.slots_per_day ?? ''} onChange={e => setForm({ ...form, slots_per_day: e.target.value ? Number(e.target.value) : undefined })} style={{ width: 90 }} />
          <button onClick={addBranch} disabled={!form.name || !form.city}>Save</button>
        </div>
      )}
      <table style={{ borderCollapse: 'collapse', width: '100%', marginBottom: 16 }}>
        <thead><tr><Th>Name</Th><Th>City</Th><Th>State</Th><Th>Phone</Th><Th>Hours</Th><Th>Slot</Th></tr></thead>
        <tbody>
          {branches.map(b => (
            <tr key={b.id} onClick={() => setSel(b.id)}
              style={{ cursor: 'pointer', background: sel === b.id ? '#f3f4f6' : undefined }}>
              <Td>{b.name}</Td><Td>{b.city}</Td><Td>{b.state}</Td><Td>{b.phone}</Td><Td>{b.open_hours}</Td>
              <Td>{b.slot_minutes}min × {b.slots_per_day}</Td>
            </tr>
          ))}
          {branches.length === 0 && <tr><Td colSpan={6}>No branches configured.</Td></tr>}
        </tbody>
      </table>

      <h3>Appointments</h3>
      <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
        <select value={sel} onChange={e => setSel(e.target.value)}>
          {branches.length === 0 && <option value="">No branches</option>}
          {branches.map(b => <option key={b.id} value={b.id}>{b.name}</option>)}
        </select>
        <input type="date" value={date} onChange={e => setDate(e.target.value)} />
      </div>
      <table style={{ borderCollapse: 'collapse', width: '100%' }}>
        <thead><tr><Th>ID</Th><Th>Member</Th><Th>Slot</Th><Th>Purpose</Th><Th>Status</Th><Th>Actions</Th></tr></thead>
        <tbody>
          {appts.map(a => (
            <tr key={a.id}>
              <Td>{a.id}</Td><Td>{a.member_id}</Td><Td>{a.slot}</Td><Td>{a.purpose}</Td>
              <Td><StatusBadge status={a.status} /></Td>
              <Td>
                <span style={{ display: 'flex', gap: 4 }}>
                  {(a.status === 'BOOKED' || a.status === 'CONFIRMED') && (
                    <>
                      <button onClick={() => transition(a.id, 'CHECKED_IN')}>Check in</button>
                      <button onClick={() => transition(a.id, 'NO_SHOW')}>No-show</button>
                      <button onClick={() => transition(a.id, 'CANCELLED')}>Cancel</button>
                    </>
                  )}
                  {a.status === 'CHECKED_IN' && (
                    <>
                      <button onClick={() => transition(a.id, 'DONE')}>Done</button>
                      <button onClick={() => transition(a.id, 'CANCELLED')}>Cancel</button>
                    </>
                  )}
                </span>
              </Td>
            </tr>
          ))}
          {appts.length === 0 && <tr><Td colSpan={6}>No appointments for this branch/date.</Td></tr>}
        </tbody>
      </table>
    </section>
  );
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    BOOKED: '#eef2ff', CONFIRMED: '#e0f2fe', CHECKED_IN: '#fef9c3',
    DONE: '#f0fdf4', NO_SHOW: '#fee2e2', CANCELLED: '#f3f4f6',
  };
  return <span style={{ background: colors[status] ?? '#f3f4f6', borderRadius: 4, padding: '2px 6px' }}>{status}</span>;
}

const Th = ({ children }: { children: React.ReactNode }) =>
  <th style={{ textAlign: 'left', borderBottom: '2px solid #e5e7eb', padding: 6 }}>{children}</th>;
const Td = ({ children, colSpan }: { children: React.ReactNode; colSpan?: number }) =>
  <td colSpan={colSpan} style={{ borderBottom: '1px solid #f3f4f6', padding: 6 }}>{children}</td>;
