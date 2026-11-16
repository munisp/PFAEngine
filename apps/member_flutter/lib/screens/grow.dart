import 'package:flutter/material.dart';
import '../api/client.dart';

/// Grow screen: retirement projection + auto-escalation + round-ups.
class GrowScreen extends StatefulWidget {
  final ApiClient api;
  final String memberId;
  const GrowScreen({super.key, required this.api, required this.memberId});

  @override
  State<GrowScreen> createState() => _GrowScreenState();
}

class _GrowScreenState extends State<GrowScreen> {
  final balance = TextEditingController();
  final monthly = TextEditingController();
  final years = TextEditingController(text: '20');
  final salary = TextEditingController();
  Map<String, dynamic>? projection;
  Map<String, dynamic>? rule;
  Map<String, dynamic>? wallet;
  String error = '';
  bool busy = false;

  @override
  void initState() {
    super.initState();
    widget.api.escalationRules(widget.memberId).then((r) {
      final rules = (r['rules'] as List?) ?? [];
      if (rules.isNotEmpty) setState(() => rule = rules.first as Map<String, dynamic>);
    }).catchError((_) {});
    widget.api.roundupWallet(widget.memberId).then((r) {
      setState(() => wallet = r['wallet'] as Map<String, dynamic>?);
    }).catchError((_) {});
  }

  Future<void> _project() async {
    setState(() { busy = true; error = ''; });
    try {
      final r = await widget.api.advisoryProject({
        'balance_kobo': (double.parse(balance.text) * 100).round(),
        'monthly_contrib_kobo': (double.parse(monthly.text) * 100).round(),
        'years_to_retire': int.parse(years.text),
        'annual_return_bps': 800,
        'salary_kobo': (double.parse(salary.text) * 100).round(),
        'replacement_bps': 5000,
      });
      setState(() => projection = r);
    } catch (e) { setState(() => error = '$e'); }
    setState(() => busy = false);
  }

  String _naira(int kobo) => '₦${(kobo / 100).toStringAsFixed(2)}';

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Grow my pension')),
      body: ListView(padding: const EdgeInsets.all(16), children: [
        Card(child: Padding(padding: const EdgeInsets.all(16), child: Column(children: [
          const Text('Retirement projection', style: TextStyle(fontWeight: FontWeight.bold)),
          TextField(controller: balance, keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: 'Current RSA balance (₦)')),
          TextField(controller: monthly, keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: 'Monthly contribution (₦)')),
          TextField(controller: years, keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: 'Years to retirement')),
          TextField(controller: salary, keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: 'Monthly salary (₦)')),
          const SizedBox(height: 12),
          FilledButton(onPressed: busy ? null : _project, child: const Text('Project')),
          if (projection != null) ...[
            const SizedBox(height: 12),
            Text('Projected: ${_naira(projection!['projected_balance_kobo'] as int)}',
                style: const TextStyle(fontSize: 18, fontWeight: FontWeight.bold)),
            Text('Expected pension: ${_naira(projection!['expected_pension_kobo'] as int)}/mo'),
            Text((projection!['on_track'] as bool)
                ? 'You are on track.'
                : 'Gap: ${_naira(projection!['gap_kobo'] as int)}/mo — add ${_naira(projection!['extra_monthly_kobo'] as int)}/mo'),
          ],
        ]))),
        Card(child: ListTile(
          title: const Text('Auto-escalation'),
          subtitle: Text(rule != null
              ? 'Rate ${(rule!['current_bps'] as int) / 100}% (cap ${(rule!['max_bps'] as int) / 100}%)'
              : 'Increase your contribution 1% every year'),
          trailing: rule == null
              ? TextButton(onPressed: busy ? null : () async {
                  final r = await widget.api.escalationEnrol(widget.memberId, 1800, 100, 2500);
                  setState(() => rule = r);
                }, child: const Text('Turn on'))
              : const Icon(Icons.check_circle, color: Colors.green),
        )),
        Card(child: ListTile(
          title: const Text('Round-ups'),
          subtitle: Text(wallet != null
              ? 'This month: ${_naira(wallet!['swept_this_month_kobo'] as int)}'
              : 'Round every spend up; the change goes to your RSA'),
          trailing: wallet == null
              ? TextButton(onPressed: busy ? null : () async {
                  final r = await widget.api.roundupOpen(widget.memberId, 10000, 500000);
                  setState(() => wallet = r);
                }, child: const Text('Turn on'))
              : const Icon(Icons.check_circle, color: Colors.green),
        )),
        if (error.isNotEmpty) Text(error, style: const TextStyle(color: Colors.red)),
      ]),
    );
  }
}
