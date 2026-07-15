import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { AdminLoginModal } from './AdminLoginModal';

describe('AdminLoginModal', () => {
  it('uses standard password-manager credential fields', () => {
    render(<AdminLoginModal open busy={false} onClose={() => {}} onLogin={vi.fn()} />);
    expect(screen.getByLabelText('用户名')).toHaveAttribute('name', 'username');
    expect(screen.getByLabelText('用户名')).toHaveAttribute('autocomplete', 'username');
    expect(screen.getByLabelText('密码')).toHaveAttribute('name', 'password');
    expect(screen.getByLabelText('密码')).toHaveAttribute('autocomplete', 'current-password');
    expect(screen.getByLabelText('密码')).toHaveAttribute('type', 'password');
  });

  it('submits credentials and closes after success', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<AdminLoginModal open busy={false} onClose={onClose} onLogin={onLogin} />);
    await user.type(screen.getByLabelText('用户名'), 'admin');
    await user.type(screen.getByLabelText('密码'), 'secret');
    await user.click(screen.getByRole('button', { name: '登录' }));
    expect(onLogin).toHaveBeenCalledWith('admin', 'secret');
    expect(onClose).toHaveBeenCalled();
  });

  it('shows login failures without clearing the username', async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn().mockRejectedValue(new Error('Invalid username or password'));
    render(<AdminLoginModal open busy={false} onClose={() => {}} onLogin={onLogin} />);
    await user.type(screen.getByLabelText('用户名'), 'admin');
    await user.type(screen.getByLabelText('密码'), 'wrong');
    await user.click(screen.getByRole('button', { name: '登录' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid username or password');
    expect(screen.getByLabelText('用户名')).toHaveValue('admin');
  });
});
