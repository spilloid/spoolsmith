using System;
using System.Collections.Generic;
using System.Drawing;
using System.Linq;
using System.Runtime.InteropServices;
using System.Threading;
using System.Windows.Forms;
using FlaUI.Core.AutomationElements;
using FlaUI.Core.Definitions;
using Xunit;

namespace SpoolSmithGui.Tests;

/// <summary>
/// Checks the real native layout at both the opening and minimum window sizes.
/// Text uses each control's actual native font, including larger bold headings.
/// Wrapped labels must fit vertically as well as horizontally.
/// </summary>
public sealed class TruncationTests : IDisposable
{
    private readonly AppFixture _fixture = new();

    private static readonly ControlType[] TextBearingTypes =
    {
        ControlType.Button, ControlType.CheckBox, ControlType.RadioButton,
        ControlType.Text, ControlType.Group, ControlType.TabItem,
    };

    // Native glyphs have descriptive accessibility names that are not painted.
    private static readonly HashSet<string> ChromeButtonNames = new(StringComparer.OrdinalIgnoreCase)
    {
        "Back by small amount", "Forward by small amount",
        "Back by large amount", "Forward by large amount",
        "Drop Down Button", "Thumb", "Minimize", "Maximize", "Restore", "Close",
    };

    public void Dispose() => _fixture.Dispose();

    public static IEnumerable<object[]> Tabs
    {
        get
        {
            foreach (var tab in new[] { "Find a printer", "Add printer", "Saved printers", "Review and apply", "Inspect", "Catalog", "Action log" })
            {
                yield return new object[] { tab, false, false };
                yield return new object[] { tab, true, false };
            }
            yield return new object[] { "Review and apply", false, true };
            yield return new object[] { "Review and apply", true, true };
        }
    }

    [StaTheory]
    [MemberData(nameof(Tabs))]
    public void Visible_captions_fit_the_window(string tabTitle, bool minimumSize, bool advanced)
    {
        if (minimumSize)
        {
            var hwnd = _fixture.MainWindow.Properties.NativeWindowHandle.Value;
            var scale = GetDpiForWindow(hwnd) / 96.0;
            Assert.True(SetWindowPos(hwnd, IntPtr.Zero, 20, 20,
                (int)Math.Round(820 * scale), (int)Math.Round(620 * scale), 0x0004));
        }
        _fixture.SelectTab(tabTitle);
        if (advanced)
        {
            _fixture.MainWindow.FindFirstDescendant(cf => cf.ByName("Advanced options"))!.AsCheckBox().Click();
        }
        // Walk schedules layout after a native resize/tab event.
        Thread.Sleep(150);
        var state = (minimumSize ? "minimum" : "default") + (advanced ? "-advanced" : "");
        _fixture.MainWindow.CaptureToFile(System.IO.Path.Combine(_fixture.RepoRoot,
            "dist", "gui-" + tabTitle.Replace(" & ", "-").Replace(" ", "-") + "-" + state + ".png"));

        var condition = TextBearingTypes
            .Select(t => (FlaUI.Core.Conditions.ConditionBase)_fixture.MainWindow.ConditionFactory.ByControlType(t))
            .Aggregate((a, b) => a.Or(b));
        var offenders = new List<string>();
        var windowBounds = _fixture.MainWindow.BoundingRectangle;
        foreach (var element in _fixture.MainWindow.FindAllDescendants(condition))
        {
            if (element.IsOffscreen) continue;
            var text = element.Properties.Name.ValueOrDefault ?? string.Empty;
            if (string.IsNullOrWhiteSpace(text)) continue;
            if (element.ControlType == ControlType.Button &&
                (ChromeButtonNames.Contains(text) ||
                 string.Equals(element.Properties.ClassName.ValueOrDefault, "ScrollBar", StringComparison.OrdinalIgnoreCase))) continue;

            var bounds = element.BoundingRectangle;
            if (bounds.Width <= 0 || bounds.Height <= 0)
            {
                offenders.Add($"{element.ControlType} '{text}' has no visible area.");
                continue;
            }
            if (!windowBounds.Contains(bounds))
            {
                offenders.Add($"{element.ControlType} '{text}' extends outside the window: {bounds}.");
            }

            using var font = NativeFont(element);
            var flags = TextFormatFlags.NoPadding | TextFormatFlags.NoPrefix;
            Size needed;
            if (element.ControlType == ControlType.Text)
            {
                // Native static labels wrap; measuring a single long line would
                // report correctly rendered explanatory paragraphs as clipped.
                needed = TextRenderer.MeasureText(text, font,
                    new Size((int)bounds.Width, int.MaxValue), flags | TextFormatFlags.WordBreak);
            }
            else
            {
                needed = TextRenderer.MeasureText(text, font, Size.Empty, flags | TextFormatFlags.SingleLine);
                needed.Width += element.ControlType switch
                {
                    ControlType.CheckBox or ControlType.RadioButton => 20,
                    ControlType.Button or ControlType.TabItem => 10,
                    ControlType.Group => 12,
                    _ => 0,
                };
            }
            if (needed.Width > bounds.Width + 3 || needed.Height > bounds.Height + 3)
            {
                offenders.Add($"{element.ControlType} '{text}' needs {needed.Width}x{needed.Height}px " +
                    $"but has {bounds.Width}x{bounds.Height}px ({font.Name} {font.SizeInPoints:0.#}pt {font.Style}).");
            }
        }

        Assert.True(offenders.Count == 0, $"Clipped caption(s) on {tabTitle} ({state}):\n" + string.Join("\n", offenders));
    }

    private static Font NativeFont(AutomationElement element)
    {
        // Tab items have no HWND of their own; the containing native tab owns
        // their font. Other native caption controls expose their own handle.
        var current = element;
        while (current != null)
        {
            var hwnd = current.Properties.NativeWindowHandle.ValueOrDefault;
            if (hwnd != IntPtr.Zero)
            {
                var hfont = SendMessage(hwnd, 0x0031 /* WM_GETFONT */, IntPtr.Zero, IntPtr.Zero);
                if (hfont != IntPtr.Zero) return Font.FromHfont(hfont);
            }
            current = current.Parent;
        }
        throw new InvalidOperationException($"Could not read the native font for '{element.Name}'.");
    }

    [DllImport("user32.dll")]
    private static extern IntPtr SendMessage(IntPtr hwnd, uint message, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll")]
    private static extern uint GetDpiForWindow(IntPtr hwnd);

    [DllImport("user32.dll", SetLastError = true)]
    [return: MarshalAs(UnmanagedType.Bool)]
    private static extern bool SetWindowPos(IntPtr hwnd, IntPtr insertAfter, int x, int y, int width, int height, uint flags);
}
