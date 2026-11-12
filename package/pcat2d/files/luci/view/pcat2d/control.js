'use strict';
'require view';
'require ui';
'require rpc';
'require poll';

var callStatus = rpc.declare({
	object: 'pcat2d',
	method: 'status',
	timeout: 8000
});

var callSet = rpc.declare({
	object: 'pcat2d',
	method: 'set',
	params: [ 'data' ],
	timeout: 20000
});

var DEFAULT = {
	fan_mode: 'smart_cool',
	fan_level: 4,
	charge_limit: 100,
	led_mode: 'on',
	led_start: '08:00',
	led_end: '22:00',
	beeper_mode: 'off',
	beeper_start: '08:00',
	beeper_end: '22:00',
	brightness: 50,
	dim_battery_sec: 60,
	dim_charger_sec: 300,
	movement: true
};

var state = null;
var statusRefs = {};

var CSS = '.pcat2d-page{max-width:920px}' +
	'.pcat2d-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px}' +
	'.pcat2d-cell{border:1px solid rgba(0,0,0,.12);border-radius:4px;padding:8px 10px;background:rgba(0,0,0,.02)}' +
	'.pcat2d-cell .k{display:block;font-size:12px;opacity:.65;margin-bottom:3px}' +
	'.pcat2d-cell .v{display:block;font-size:15px;font-weight:600}' +
	'.pcat2d-range{display:flex;align-items:center;gap:10px}' +
	'.pcat2d-range input{flex:1 1 auto}' +
	'.pcat2d-range-value{min-width:56px;text-align:right;font-variant-numeric:tabular-nums}' +
	'.pcat2d-time-range{display:flex;align-items:center;gap:6px}' +
	'.pcat2d-time{width:120px !important}' +
	'.pcat2d-number{width:90px !important}' +
	'.pcat2d-hint{opacity:.7;font-size:12px}' +
	'.pcat2d-actions{display:flex;justify-content:flex-end;gap:10px;padding:14px 0}';

function text(id, value) {
	if (statusRefs[id])
		statusRefs[id].textContent = value;
}

function cell(title) {
	var v = E('span', { 'class': 'v' }, [ '-' ]);
	statusRefs[title] = v;
	return E('div', { 'class': 'pcat2d-cell' }, [
		E('span', { 'class': 'k' }, [ title ]),
		v
	]);
}

function row(title, widget) {
	return E('div', { 'class': 'cbi-value' }, [
		E('label', { 'class': 'cbi-value-title' }, [ title ]),
		E('div', { 'class': 'cbi-value-field' }, [ widget ])
	]);
}

function sel(options, value, onchange) {
	var children = [];
	for (var i = 0; i < options.length; i++)
		children.push(E('option', { 'value': options[i][0] }, [ options[i][1] ]));
	var el = E('select', {
		'class': 'cbi-input-select',
		'change': function(ev) { onchange(ev.target.value); }
	}, children);
	el.value = value;
	return el;
}

function rangeInput(min, max, step, value, suffix, onchange) {
	var out = E('span', { 'class': 'pcat2d-range-value' }, [ String(value) + (suffix || '') ]);
	var input = E('input', {
		'type': 'range',
		'class': 'cbi-input-range',
		'min': String(min),
		'max': String(max),
		'step': String(step),
		'value': String(value),
		'input': function(ev) {
			out.textContent = ev.target.value + (suffix || '');
			onchange(parseInt(ev.target.value, 10));
		}
	});
	return E('div', { 'class': 'pcat2d-range' }, [ input, out ]);
}

function timeInput(value, onchange) {
	return E('input', {
		'type': 'time',
		'class': 'cbi-input-text pcat2d-time',
		'value': value || '08:00',
		'change': function(ev) { onchange(ev.target.value); }
	});
}

function numberInput(value, min, max, onchange) {
	return E('input', {
		'type': 'number',
		'class': 'cbi-input-text pcat2d-number',
		'min': String(min),
		'max': String(max),
		'step': '1',
		'value': String(value),
		'change': function(ev) { onchange(parseInt(ev.target.value, 10) || 0); }
	});
}

function applyStatus(s) {
	if (!s)
		return;
	var fan = '智能散热';
	if (s.fan_mode === 'manual')
		fan = '手动 ' + s.fan_level + '/9';
	else if (s.fan_mode === 'smart_quiet')
		fan = '静音';
	text('风扇', fan + ' · ' + (s.fan_rpm || 0) + ' RPM');
	text('板载温度', (s.board_temp_c || 0) + ' °C');
	text('电池', (s.battery_pct || 0) + '% · ' + (s.battery_mv || 0) + ' mV');
	text('充电器', s.charger_online ? '已连接' : '未连接');
	text('屏幕', s.awake ? ('点亮 · 亮度 ' + s.brightness + '%') : '息屏');
	text('pcat2d 服务', '运行中' + (s.mcu_err ? ' · MCU 警告' : ''));
}

function buildSections(rebuild) {
	return [
		E('h2', {}, [ 'Photonicat2 控制页' ]),
		E('div', { 'class': 'cbi-map-descr' }, [ '小屏与设备控制的统一设置。状态每 5 秒刷新，修改后点「保存并应用」。' ]),

		E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ '设备状态' ]),
			E('div', { 'class': 'pcat2d-grid' }, [
				cell('风扇'), cell('板载温度'), cell('电池'),
				cell('充电器'), cell('屏幕'), cell('pcat2d 服务')
			])
		]),

		E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ '屏幕设置' ]),
			row('亮度', rangeInput(0, 100, 5, state.brightness, '%', function(v) { state.brightness = v; })),
			row('电池供电息屏（秒）', numberInput(state.dim_battery_sec, 0, 86400, function(v) { state.dim_battery_sec = v; })),
			row('充电息屏（秒）', numberInput(state.dim_charger_sec, 0, 86400, function(v) { state.dim_charger_sec = v; })),
			row('移动唤醒', sel([ [ '1', '启用' ], [ '0', '禁用' ] ], state.movement ? '1' : '0',
				function(v) { state.movement = (v === '1'); }))
		]),

		E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ '风扇控制' ]),
			row('模式', sel([
				[ 'smart_cool', '智能散热' ],
				[ 'smart_quiet', '静音' ],
				[ 'manual', '手动固定' ]
			], state.fan_mode, function(v) { state.fan_mode = v; })),
			row('手动档位', rangeInput(1, 9, 1, state.fan_level, ' 档', function(v) { state.fan_level = v; })),
			E('div', { 'class': 'cbi-value' }, [
				E('span', { 'class': 'pcat2d-hint' }, [ '智能/静音由 MCU 按板温自动调速，手动为固定 1-9 档。' ])
			])
		]),

		E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ '充电上限' ]),
			row('充电上限', rangeInput(80, 100, 5, state.charge_limit, '%', function(v) { state.charge_limit = v; })),
			E('div', { 'class': 'cbi-value' }, [
				E('span', { 'class': 'pcat2d-hint' }, [ '通过 MCU 设置，适合长期插电使用以保护电池。' ])
			])
		]),

		E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ '电源 LED' ]),
			row('模式', sel([ [ 'off', '关闭' ], [ 'on', '常亮' ], [ 'timed', '定时' ] ], state.led_mode,
				function(v) { state.led_mode = v; })),
			row('定时时段', E('div', { 'class': 'pcat2d-time-range' }, [
				timeInput(state.led_start, function(v) { state.led_start = v; }),
				E('span', {}, [ '至' ]),
				timeInput(state.led_end, function(v) { state.led_end = v; })
			]))
		]),

		E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, [ '蜂鸣器' ]),
			row('模式', sel([ [ 'off', '关闭' ], [ 'on', '常响' ], [ 'timed', '定时' ] ], state.beeper_mode,
				function(v) { state.beeper_mode = v; })),
			row('定时时段', E('div', { 'class': 'pcat2d-time-range' }, [
				timeInput(state.beeper_start, function(v) { state.beeper_start = v; }),
				E('span', {}, [ '至' ]),
				timeInput(state.beeper_end, function(v) { state.beeper_end = v; })
			]))
		]),

		E('div', { 'class': 'pcat2d-actions' }, [
			E('button', {
				'class': 'btn cbi-button',
				'click': function() {
					state.fan_mode = DEFAULT.fan_mode;
					state.fan_level = DEFAULT.fan_level;
					state.charge_limit = DEFAULT.charge_limit;
					state.led_mode = DEFAULT.led_mode;
					state.led_start = DEFAULT.led_start;
					state.led_end = DEFAULT.led_end;
					state.beeper_mode = DEFAULT.beeper_mode;
					state.beeper_start = DEFAULT.beeper_start;
					state.beeper_end = DEFAULT.beeper_end;
					state.brightness = DEFAULT.brightness;
					state.dim_battery_sec = DEFAULT.dim_battery_sec;
					state.dim_charger_sec = DEFAULT.dim_charger_sec;
					state.movement = DEFAULT.movement;
					if (rebuild)
						rebuild();
					ui.addNotification(null, E('p', {}, [ '已填入默认值，点「保存并应用」生效。' ]));
				}
			}, [ '恢复默认' ]),
			E('button', {
				'class': 'btn cbi-button cbi-button-apply',
				'click': function() {
					var payload = {
						fan_mode: state.fan_mode,
						fan_level: parseInt(state.fan_level, 10) || 1,
						charge_limit: parseInt(state.charge_limit, 10) || 100,
						led_mode: state.led_mode,
						led_start: state.led_start,
						led_end: state.led_end,
						beeper_mode: state.beeper_mode,
						beeper_start: state.beeper_start,
						beeper_end: state.beeper_end,
						brightness: parseInt(state.brightness, 10) || 0,
						dim_battery_sec: parseInt(state.dim_battery_sec, 10) || 0,
						dim_charger_sec: parseInt(state.dim_charger_sec, 10) || 0,
						movement: !!state.movement
					};
					callSet(payload).then(function(res) {
						if (res && res.status) {
							state = res.status;
							applyStatus(state);
						}
						ui.addNotification(null, E('p', {}, [ (res && res.msg) || '已保存并应用' ]));
					}, function(err) {
						ui.addNotification(null, E('p', {}, [
							'保存失败: ' + (err && err.message ? err.message : err)
						]), 'error');
					});
				}
			}, [ '保存并应用' ])
		])
	];
}

return view.extend({
	load: function() {
		return callStatus().then(function(res) {
			state = (res && res.status) ? res.status : DEFAULT;
			return state;
		});
	},

	render: function() {
		if (!state)
			state = DEFAULT;

		var root = E('div', { 'class': 'pcat2d-page' });

		function rebuild() {
			while (root.firstChild)
				root.removeChild(root.firstChild);
			var secs = buildSections(rebuild);
			for (var i = 0; i < secs.length; i++)
				root.appendChild(secs[i]);
			applyStatus(state);
		}

		root.appendChild(E('style', {}, [ CSS ]));
		var secs = buildSections(rebuild);
		for (var i = 0; i < secs.length; i++)
			root.appendChild(secs[i]);
		applyStatus(state);

		poll.add(function() {
			return callStatus().then(function(res) {
				if (res && res.status)
					applyStatus(res.status);
			}, function() {});
		}, 5);

		return root;
	}
});
